package updater

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

type fakeInstallation struct {
	calls    []string
	fail     string
	prepared chan struct{}
	resume   chan struct{}
}

type statusFailureInstallation struct {
	*fakeInstallation
	statusPath string
}

func (f *statusFailureInstallation) Prepare(_ context.Context, p Pin, progress func(string)) (Transaction, error) {
	if err := os.Remove(f.statusPath); err != nil {
		return Transaction{}, err
	}
	if err := os.Mkdir(f.statusPath, 0700); err != nil {
		return Transaction{}, err
	}
	progress("verifying") // A directory cannot be replaced by the status JSON file.
	if err := os.Remove(f.statusPath); err != nil {
		return Transaction{}, err
	}
	return Transaction{OldRuntime: "/old", NewRuntime: "/new", Pin: p}, f.step("prepare")
}

func TestProgressStatusFailureDoesNotStopApp(t *testing.T) {
	installation := &fakeInstallation{}
	engine := transactionEngine(t, installation)
	engine.Installation = &statusFailureInstallation{installation, filepath.Join(engine.Config.StatePath, statusFile)}
	if err := engine.Run(context.Background()); !errors.Is(err, syscall.EISDIR) {
		t.Fatalf("Run error = %v, want the progress persistence failure", err)
	}
	if !reflect.DeepEqual(installation.calls, []string{"prepare"}) {
		t.Fatalf("status failure reached installation mutation: %v", installation.calls)
	}
	if journal, err := engine.journal(); err != nil || journal.Phase != "" {
		t.Fatalf("status failure created a recovery transaction: %+v, %v", journal, err)
	}
	if status := engine.Config.Status(); status.State != "failed" {
		t.Fatalf("retry status = %+v, want failed", status)
	}
}

func (f *fakeInstallation) step(s string) error {
	f.calls = append(f.calls, s)
	if f.fail == s {
		return errors.New(s + " failed")
	}
	return nil
}
func (f *fakeInstallation) Prepare(ctx context.Context, p Pin, progress func(string)) (Transaction, error) {
	if f.prepared != nil {
		close(f.prepared)
		<-f.resume
	}
	progress("verifying")
	return Transaction{OldRuntime: "/owned/releases/v0.51.5", NewRuntime: "/owned/releases/" + p.Tag, OldCurrent: "releases/v0.51.5", Pin: p}, f.step("prepare")
}
func (f *fakeInstallation) Validate(t Transaction) error {
	if t.OldRuntime == "" || t.NewRuntime == "" {
		return errors.New("invalid transaction")
	}
	return nil
}
func (f *fakeInstallation) Stop(context.Context, Transaction) error     { return f.step("stop") }
func (f *fakeInstallation) Activate(context.Context, Transaction) error { return f.step("activate") }
func (f *fakeInstallation) Restore(context.Context, Transaction) error  { return f.step("restore") }
func (f *fakeInstallation) Start(_ context.Context, _ Transaction, rollback bool) error {
	if rollback {
		return f.step("start-old")
	}
	return f.step("start-new")
}
func (f *fakeInstallation) Ready(_ context.Context, _ Transaction, rollback bool) error {
	if rollback {
		return f.step("ready-old")
	}
	return f.step("ready-new")
}
func transactionEngine(t *testing.T, f *fakeInstallation) Engine {
	t.Helper()
	c := Config{Installed: "0.51.5", StatePath: privateState(t)}
	p := fixturePin("v0.51.9")
	if e := c.Save(Status{State: "available", Installed: c.Installed, Available: "0.51.9", Pin: &p}); e != nil {
		t.Fatal(e)
	}
	return Engine{Config: c, Installation: f}
}
func TestInstallSuccessRecordsRunningVersion(t *testing.T) {
	f := &fakeInstallation{}
	e := transactionEngine(t, f)
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.calls, []string{"prepare", "stop", "activate", "start-new", "ready-new"}) {
		t.Fatal(f.calls)
	}
	c := e.Config
	c.Installed = "0.51.9"
	s := c.Status()
	if s.State != "updated" || s.Installed != "0.51.9" {
		t.Fatalf("%+v", s)
	}
	j, err := e.journal()
	if err != nil || j.Phase != "complete" {
		t.Fatalf("%+v %v", j, err)
	}
}
func TestFailedInstallRestoresAndVerifiesOldVersion(t *testing.T) {
	for _, phase := range []string{"stop", "activate", "start-new", "ready-new"} {
		t.Run(phase, func(t *testing.T) {
			f := &fakeInstallation{fail: phase}
			e := transactionEngine(t, f)
			if e.Run(context.Background()) == nil {
				t.Fatal("unexpected success")
			}
			want := []string{"restore", "start-old", "ready-old"}
			if !reflect.DeepEqual(f.calls[len(f.calls)-3:], want) {
				t.Fatal(f.calls)
			}
			if e.Config.Status().State != "rolled_back" {
				t.Fatalf("%+v", e.Config.Status())
			}
		})
	}
}
func TestFailedDownloadDoesNotStopApp(t *testing.T) {
	f := &fakeInstallation{fail: "prepare"}
	e := transactionEngine(t, f)
	if e.Run(context.Background()) == nil {
		t.Fatal("unexpected success")
	}
	if !reflect.DeepEqual(f.calls, []string{"prepare"}) || e.Config.Status().State != "failed" {
		t.Fatal(f.calls, e.Config.Status())
	}
}
func TestInterruptedUpdateRecoversEveryMutationPhase(t *testing.T) {
	for _, phase := range []string{"prepared", "switching", "activated", "restarting"} {
		f := &fakeInstallation{}
		e := transactionEngine(t, f)
		j := Transaction{Phase: phase, OldRuntime: "/old", NewRuntime: "/new", Pin: fixturePin("v0.51.9")}
		if err := e.saveJournal(j); err != nil {
			t.Fatal(err)
		}
		if err := e.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(f.calls, []string{"restore", "start-old", "ready-old"}) {
			t.Fatal(phase, f.calls)
		}
		f.calls = nil
		if err := e.Recover(context.Background()); err != nil || len(f.calls) != 0 {
			t.Fatal("recovery not idempotent", err, f.calls)
		}
	}
}
func TestConcurrentUpdateAndCheckDoNotOverwriteWorkerStatus(t *testing.T) {
	f := &fakeInstallation{prepared: make(chan struct{}), resume: make(chan struct{})}
	e := transactionEngine(t, f)
	done := make(chan error, 1)
	go func() { done <- e.Run(context.Background()) }()
	<-f.prepared
	if e.Run(context.Background()) == nil {
		t.Fatal("concurrent install accepted")
	}
	e.Config.Source = sourceFunc(func(context.Context) (Pin, string, error) { t.Fatal("checked during install"); return Pin{}, "", nil })
	s, err := e.Config.Check(context.Background(), true)
	if err != nil || s.State != "downloading" {
		t.Fatalf("%+v %v", s, err)
	}
	close(f.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
