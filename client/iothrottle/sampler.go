package iothrottle

import (
	"context"
	"log"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
)

// SampleInterval is the tick period for the sampler goroutine. Kept at
// 1 Hz because that matches the SmoothingWindow constant: 5 samples =
// 5 seconds of averaging. Faster sampling would need a proportionally
// larger window to stay stable.
const SampleInterval = 1 * time.Second

// StartSampler runs a goroutine that reads iowait% every SampleInterval
// and feeds it into the given Throttle. It maintains its own state for
// the cumulative-jiffies delta, so it does not contend with
// workerstats.CollectWorkerStats (which is called on a separate
// cadence, from the ping loop).
//
// Returns immediately. The goroutine stops when ctx is done. Failures
// to read cpu.Times are logged but non-fatal — a missed sample just
// means the throttle sees no new data for that tick.
func StartSampler(ctx context.Context, t *Throttle) {
	go func() {
		ticker := time.NewTicker(SampleInterval)
		defer ticker.Stop()

		var last []cpu.TimesStat
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			curr, err := cpu.Times(false)
			if err != nil {
				log.Printf("⚠️ iothrottle sampler: cpu.Times error: %v", err)
				continue
			}
			if len(last) == len(curr) && len(curr) > 0 {
				totalDelta := 0.0
				iowaitDelta := 0.0
				for i := range curr {
					p, c := last[i], curr[i]
					pTot := p.User + p.System + p.Idle + p.Nice + p.Iowait + p.Irq + p.Softirq + p.Steal + p.Guest + p.GuestNice
					cTot := c.User + c.System + c.Idle + c.Nice + c.Iowait + c.Irq + c.Softirq + c.Steal + c.Guest + c.GuestNice
					if delta := cTot - pTot; delta > 0 {
						totalDelta += delta
						iowaitDelta += c.Iowait - p.Iowait
					}
				}
				if totalDelta > 0 {
					pct := float32((iowaitDelta / totalDelta) * 100.0)
					if pct < 0 {
						pct = 0
					}
					t.Sample(pct)
				}
			}
			last = curr
		}
	}()
}
