package iothrottle

import (
	"context"
	"log"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
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
// When a non-nil PeakTracker is passed, the sampler additionally folds
// per-tick cpu%, mem%, and iowait% into it so the ping loop can report
// sub-ping-interval peaks to the server. Pass nil to keep only the
// throttle feed (older behaviour). A cpu% or mem% read error skips
// that dimension for the tick but does not stop the sampler.
//
// Returns immediately. The goroutine stops when ctx is done. Failures
// to read cpu.Times are logged but non-fatal — a missed sample just
// means the throttle sees no new data for that tick.
func StartSampler(ctx context.Context, t *Throttle, peaks *PeakTracker) {
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
				busyDelta := 0.0
				for i := range curr {
					p, c := last[i], curr[i]
					pTot := p.User + p.System + p.Idle + p.Nice + p.Iowait + p.Irq + p.Softirq + p.Steal + p.Guest + p.GuestNice
					cTot := c.User + c.System + c.Idle + c.Nice + c.Iowait + c.Irq + c.Softirq + c.Steal + c.Guest + c.GuestNice
					if delta := cTot - pTot; delta > 0 {
						totalDelta += delta
						iowaitDelta += c.Iowait - p.Iowait
						// "busy" is what CollectWorkerStats reports as
						// cpu_usage_percent: everything except idle +
						// iowait. Keeps the peak comparable to the
						// same-named current-value field.
						busyDelta += (c.User + c.System + c.Nice + c.Irq + c.Softirq + c.Steal) -
							(p.User + p.System + p.Nice + p.Irq + p.Softirq + p.Steal)
					}
				}
				if totalDelta > 0 {
					iowaitPct := float32((iowaitDelta / totalDelta) * 100.0)
					if iowaitPct < 0 {
						iowaitPct = 0
					}
					t.Sample(iowaitPct)

					if peaks != nil {
						cpuPct := float32((busyDelta / totalDelta) * 100.0)
						if cpuPct < 0 {
							cpuPct = 0
						}
						var memPct float32
						if v, err := mem.VirtualMemory(); err == nil {
							memPct = float32(v.UsedPercent)
						}
						// MAX across every reachable partition. workerstats
						// walks the same list per ping; we recompute here
						// so the tracker sees fresh values every second
						// (a full-per-ping snapshot would leak up to 5s of
						// truth). "true" reports all partitions including
						// pseudo-filesystems; the disk.Usage call skips
						// those it can't stat, so a lost-mount or a
						// permission-denied path contributes 0 rather
						// than poisoning the max.
						var diskPct float32
						if parts, err := disk.Partitions(true); err == nil {
							for _, part := range parts {
								if u, err := disk.Usage(part.Mountpoint); err == nil {
									if p := float32(u.UsedPercent); p > diskPct {
										diskPct = p
									}
								}
							}
						}
						peaks.Observe(cpuPct, memPct, iowaitPct, diskPct)
					}
				}
			}
			last = curr
		}
	}()
}
