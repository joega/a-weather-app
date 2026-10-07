package updater

import (
	"testing"

	"github.com/joega/a-weather-app/internal/safeio"
)

func TestProgressRequiresLiveWorkerLock(t *testing.T) {
	l, pin := managedFixture(t)
	c := l.Config
	if e := c.Save(Status{State: "downloading", Installed: c.Installed, Available: pin.Tag[1:], Pin: &pin}); e != nil {
		t.Fatal(e)
	}
	if s := c.Status(); s.State != "failed" || s.Pin == nil {
		t.Fatal("orphaned download left disabled update controls", s)
	}
	d, e := safeio.OpenDir(c.StatePath, false)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	f, e := d.Lock("update-install.lock")
	if e != nil {
		t.Fatal(e)
	}
	if s := c.Status(); s.State != "downloading" {
		t.Fatal("live worker treated as interrupted", s)
	}
	f.Close()
	if s := c.Status(); s.State != "failed" {
		t.Fatal("terminated worker not detected", s)
	}
}
