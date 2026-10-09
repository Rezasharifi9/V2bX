package counter

import (
	"errors"
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
)

func TestCollectKeepsCountersOnPersistenceFailure(t *testing.T) {
	c := NewTrafficCounter()
	c.Tx("uuid", 100)
	c.Rx("uuid", 200)
	_, err := c.Collect(func(string) int { return 7 }, func([]panel.UserTraffic) error { return errors.New("disk full") })
	if err == nil || c.GetUpCount("uuid") != 100 || c.GetDownCount("uuid") != 200 {
		t.Fatal("persistence failure lost counters")
	}
}

func TestCollectPreservesTrafficArrivingDuringSave(t *testing.T) {
	c := NewTrafficCounter()
	c.Tx("uuid", 100)
	c.Rx("uuid", 200)
	batch, err := c.Collect(func(string) int { return 7 }, func(batch []panel.UserTraffic) error {
		if len(batch) != 1 || batch[0].Upload != 100 || batch[0].Download != 200 {
			t.Fatalf("unexpected snapshot %+v", batch)
		}
		c.Tx("uuid", 30)
		c.Rx("uuid", 40)
		return nil
	})
	if err != nil || len(batch) != 1 {
		t.Fatalf("collect failed: %v", err)
	}
	if c.GetUpCount("uuid") != 30 || c.GetDownCount("uuid") != 40 {
		t.Fatal("new traffic lost during save")
	}
}
