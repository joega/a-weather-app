package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
)

const recoveryBudget = 120 * time.Second
const journalFile = "update-transaction.json"

// ErrBusy indicates that another process owns the installation transaction lock.
var ErrBusy = errors.New("an update is already in progress")

// Transaction is durable before the running app is stopped. The implementation
// validates its paths and revisions again before every mutation or recovery.
type Transaction struct {
	Phase       string `json:"phase"`
	OldRuntime  string `json:"old_runtime"`
	RunningRoot string `json:"running_root,omitempty"`
	OldVersion  string `json:"old_version"`
	NewRuntime  string `json:"new_runtime"`
	OldCurrent  string `json:"old_current"`
	PluginRoot  string `json:"plugin_root"`
	OldCommit   string `json:"old_commit"`
	NewCommit   string `json:"new_commit"`
	Pin         Pin    `json:"pin"`
}

// Installation separates platform operations from the durable transaction so
// shutdown, activation, restart and recovery can be tested without the desktop.
type Installation interface {
	Prepare(context.Context, Pin, func(string)) (Transaction, error)
	Validate(Transaction) error
	Stop(context.Context, Transaction) error
	Activate(context.Context, Transaction) error
	Restore(context.Context, Transaction) error
	Start(context.Context, Transaction, bool) error
	Ready(context.Context, Transaction, bool) error
}

// Engine runs durable installation/recovery transactions through Installation.
// Config and Installation must describe the same private state and runtime.
type Engine struct {
	Config       Config
	Installation Installation
}

type installationLocker interface{ LockInstallation() (func(), error) }

func (e Engine) installationLock() (func(), error) {
	if l, ok := e.Installation.(installationLocker); ok {
		return l.LockInstallation()
	}
	return func() {}, nil
}

func (e Engine) journal() (Transaction, error) {
	v, err := safeio.Read(e.Config.StatePath+"/"+journalFile, 16384)
	if err != nil || v == nil {
		return Transaction{}, err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return Transaction{}, err
	}
	var t Transaction
	if err = json.Unmarshal(raw, &t); err != nil {
		return t, err
	}
	switch t.Phase {
	case "prepared", "switching", "activated", "restarting", "complete", "rolled_back":
	default:
		return t, errors.New("invalid update journal phase")
	}
	if err = t.Pin.Validate(); err != nil {
		return t, err
	}
	if t.Phase == "complete" || t.Phase == "rolled_back" {
		return t, nil
	}
	return t, e.Installation.Validate(t)
}
func (e Engine) saveJournal(t Transaction) error {
	return safeio.Write(e.Config.StatePath+"/"+journalFile, t, 16384)
}

func (e Engine) status(state, message string, pin *Pin) error {
	s := e.Config.Status()
	s.State = state
	s.Message = message
	if pin != nil {
		s.Pin = pin
		s.Available = pin.Tag[1:]
	}
	return e.Config.Save(s)
}

func (e Engine) lock() (*safeio.Directory, func(), error) {
	d, err := safeio.OpenDir(e.Config.StatePath, true)
	if err != nil {
		return nil, nil, err
	}
	f, err := d.Lock("update-install.lock")
	if err != nil {
		d.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, nil, ErrBusy
		}
		return nil, nil, err
	}
	return d, func() { f.Close(); d.Close() }, nil
}

// NeedsRecovery reports an unfinished durable journal without changing installation.
func (e Engine) NeedsRecovery() (bool, error) {
	_, unlock, err := e.lock()
	if errors.Is(err, ErrBusy) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer unlock()
	t, err := e.journal()
	if err != nil {
		return false, err
	}
	return t.Phase != "" && t.Phase != "complete" && t.Phase != "rolled_back", nil
}

// Recover uses the same lock as installation. A running worker prevents another
// process from treating its live transaction as an interrupted update.
func (e Engine) Recover(ctx context.Context) error {
	_, unlock, err := e.lock()
	if err != nil {
		return err
	}
	defer unlock()
	finish, err := e.installationLock()
	if err != nil {
		return err
	}
	defer finish()
	t, err := e.journal()
	if err != nil {
		return err
	}
	if t.Phase == "" || t.Phase == "complete" || t.Phase == "rolled_back" {
		return nil
	}
	return e.rollback(ctx, t, errors.New("the previous update was interrupted"))
}

// Run installs an available verified pin under exclusive transaction locks.
// It journals before stopping the app; subsequent failures attempt rollback
// with an independent recovery deadline. Status-persistence errors are retained.
func (e Engine) Run(ctx context.Context) (err error) {
	if e.Config.Development {
		return errors.New("development checkouts are built locally")
	}
	if e.Installation == nil {
		return errors.New("installer unavailable")
	}
	defer func() {
		if err != nil && !errors.Is(err, ErrBusy) {
			s := e.Config.Status()
			if s.State != "failed" && s.State != "rolled_back" {
				err = errors.Join(err, e.status("failed", "Update could not finish: "+boundedError(err)+". Try again.", s.Pin))
			}
		}
	}()
	_, unlock, err := e.lock()
	if err != nil {
		return err
	}
	defer unlock()
	finish, err := e.installationLock()
	if err != nil {
		return err
	}
	defer finish()
	t, err := e.journal()
	if err != nil {
		return err
	}
	if t.Phase != "" && t.Phase != "complete" && t.Phase != "rolled_back" {
		return e.rollback(ctx, t, errors.New("the previous update was interrupted; try the update again"))
	}
	s := e.Config.Status()
	if s.Pin == nil {
		return errors.New("check for updates before installing")
	}
	n, err := Compare(e.Config.Installed, s.Pin.Tag)
	if err != nil || n >= 0 {
		return errors.New("no newer supported release is available")
	}
	pin := *s.Pin
	if err = e.status("downloading", "Downloading the new version…", &pin); err != nil {
		return err
	}
	var progressErr error
	progress := func(phase string) {
		message := "Downloading the new version…"
		if phase == "verifying" {
			message = "Verifying the downloaded release…"
		}
		progressErr = errors.Join(progressErr, e.status(phase, message, &pin))
	}
	t, err = e.Installation.Prepare(ctx, pin, progress)
	err = errors.Join(err, progressErr)
	if err != nil {
		return errors.Join(err, e.status("failed", "Update was not installed: "+boundedError(err), &pin))
	}
	t.Pin = pin
	t.OldVersion = e.Config.Installed
	t.Phase = "prepared"
	if err = e.Installation.Validate(t); err != nil {
		return err
	}
	if err = e.saveJournal(t); err != nil {
		return err
	}
	// From this point all errors recover the prior installation. Recovery gets a
	// fresh context even if cancellation caused the original operation to fail.
	defer func() {
		if err != nil {
			recoveryCtx, cancel := context.WithTimeout(context.Background(), recoveryBudget)
			defer cancel()
			err = errors.Join(err, e.rollback(recoveryCtx, t, err))
		}
	}()
	if err = e.status("restarting", "Stopping weather effects and preparing to restart…", &pin); err != nil {
		return err
	}
	if err = e.Installation.Stop(ctx, t); err != nil {
		return err
	}
	t.Phase = "switching"
	if err = e.saveJournal(t); err != nil {
		return err
	}
	if err = e.Installation.Activate(ctx, t); err != nil {
		return err
	}
	t.Phase = "activated"
	if err = e.saveJournal(t); err != nil {
		return err
	}
	if err = e.Installation.Start(ctx, t, false); err != nil {
		return err
	}
	t.Phase = "restarting"
	if err = e.saveJournal(t); err != nil {
		return err
	}
	if err = e.Installation.Ready(ctx, t, false); err != nil {
		return err
	}
	t.Phase = "complete"
	if err = e.saveJournal(t); err != nil {
		return err
	}
	installed := e.Config
	installed.Installed = pin.Tag[1:]
	return installed.Save(Status{State: "updated", Installed: installed.Installed, Message: "Updated successfully", CheckedAt: installed.now().Unix()})
}

func (e Engine) rollback(ctx context.Context, t Transaction, cause error) error {
	if err := e.Installation.Validate(t); err != nil {
		return err
	}
	// Restore must stop any new service before switching back and be idempotent.
	if err := e.Installation.Restore(ctx, t); err != nil {
		return errors.Join(err, e.status("failed", "Could not restore the previous version: "+boundedError(err), &t.Pin))
	}
	if err := e.Installation.Start(ctx, t, true); err != nil {
		return err
	}
	if err := e.Installation.Ready(ctx, t, true); err != nil {
		return err
	}
	t.Phase = "rolled_back"
	if err := e.saveJournal(t); err != nil {
		return err
	}
	restored := e
	if t.OldVersion != "" {
		restored.Config.Installed = t.OldVersion
	}
	message := "Restored the previous version. " + boundedError(cause) + ". You can retry the update."
	if w, ok := e.Installation.(interface{ RecoveryWarning() string }); ok && w.RecoveryWarning() != "" {
		message = "Restored the previous version. " + w.RecoveryWarning()
	}
	return restored.status("rolled_back", message, &t.Pin)
}

func boundedError(err error) string {
	v := fmt.Sprint(err)
	if len(v) > 240 {
		v = v[:240]
	}
	return v
}
