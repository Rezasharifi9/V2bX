package counter

import "github.com/InazumaV/V2bX/api/panel"

// Collect snapshots counters, persists that snapshot, then subtracts only the
// saved bytes. Traffic arriving during the disk write remains in the counters.
// The caller holds its user-map lock while this function runs.
func (c *TrafficCounter) Collect(uidFor func(string) int, persist func([]panel.UserTraffic) error) ([]panel.UserTraffic, error) {
	type snapshot struct {
		storage  *TrafficStorage
		up, down int64
	}
	var snapshots []snapshot
	var traffic []panel.UserTraffic
	c.Counters.Range(func(key, value interface{}) bool {
		uid := uidFor(key.(string))
		if uid <= 0 {
			return true
		}
		storage := value.(*TrafficStorage)
		up, down := storage.UpCounter.Load(), storage.DownCounter.Load()
		if up == 0 && down == 0 {
			return true
		}
		snapshots = append(snapshots, snapshot{storage, up, down})
		traffic = append(traffic, panel.UserTraffic{UID: uid, Upload: up, Download: down})
		return true
	})
	if len(traffic) == 0 {
		return nil, nil
	}
	if err := persist(traffic); err != nil {
		return nil, err
	}
	for _, saved := range snapshots {
		saved.storage.UpCounter.Add(-saved.up)
		saved.storage.DownCounter.Add(-saved.down)
	}
	return traffic, nil
}
