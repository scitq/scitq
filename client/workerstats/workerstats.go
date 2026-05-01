package workerstats

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

var lastCPUTimes []cpu.TimesStat

// isVirtualNIC returns true for interface names that don't correspond to
// real external traffic — loopback, docker/podman bridges, veth pairs,
// wireguard/openvpn tunnels, libvirt/vmware vnets. Including them in
// "sent"/"received" totals produces wildly inflated numbers because
// loopback double-counts every byte and bridge traffic between
// containers and the host appears as external bandwidth.
func isVirtualNIC(name string) bool {
	if name == "lo" {
		return true
	}
	prefixes := []string{
		"docker", "br-", "veth", "cni",
		"wg", "tun", "tap",
		"vmnet", "virbr", "vnet",
		"flannel", "cali", "weave",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// WorkerStats matches your proto definition
// adjust the field tags if you use protobuf-generated structs directly

type WorkerStats struct {
	CPUUsagePercent float32
	MemUsagePercent float32
	Load1Min        float32
	IOWaitPercent   float32
	Disks           []DiskUsage
	DiskIO          DiskIOStats
	NetIO           NetIOStats
	NumCPUs         int32
}

type DiskUsage struct {
	DeviceName   string
	UsagePercent float32
}

type DiskIOStats struct {
	ReadBytesTotal  int64
	WriteBytesTotal int64
	ReadBytesRate   float32
	WriteBytesRate  float32
}

type NetIOStats struct {
	RecvBytesTotal int64
	SentBytesTotal int64
	RecvBytesRate  float32
	SentBytesRate  float32
}

var (
	statsMu    sync.Mutex
	lastDiskIO map[string]disk.IOCountersStat
	lastNetIO  map[string]net.IOCountersStat
	lastTime   time.Time
)

// findBackingPartition returns the partition whose mountpoint is the deepest prefix of the given path.
// This handles cases where /var is part of /, or is a bind/overlay onto /scratch, etc.
func findBackingPartition(parts []disk.PartitionStat, path string) (disk.PartitionStat, bool) {
	bestLen := -1
	var best disk.PartitionStat
	for _, p := range parts {
		mp := p.Mountpoint
		if mp == "" {
			continue
		}
		// Ensure we only match whole path segments ("/var" should not match "/varlib")
		if strings.HasPrefix(path, mp) && (len(path) == len(mp) || strings.HasPrefix(path[len(mp):], "/")) {
			if l := len(mp); l > bestLen {
				bestLen = l
				best = p
			}
		}
	}
	if bestLen >= 0 {
		return best, true
	}
	return disk.PartitionStat{}, false
}

func CollectWorkerStats() (*WorkerStats, error) {
	statsMu.Lock()
	defer statsMu.Unlock()

	var stats WorkerStats
	var err error

	stats.NumCPUs = int32(runtime.NumCPU())

	cpuPercents, err := cpu.Percent(0, false)
	if err != nil {
		return nil, err
	}
	if len(cpuPercents) > 0 {
		stats.CPUUsagePercent = float32(cpuPercents[0])
	}

	vmem, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}
	stats.MemUsagePercent = float32(vmem.UsedPercent)

	loadAvg, err := load.Avg()
	if err != nil {
		return nil, err
	}
	stats.Load1Min = float32(loadAvg.Load1)

	/*
		times, err := cpu.Times(false)
		if err == nil && len(times) > 0 {
			if len(lastCPUTimes) == len(times) {
				// Calculate delta-based iowait percent
				totalDelta := float64(0)
				iowaitDelta := float64(0)
				for i := range times {
					prev := lastCPUTimes[i]
					curr := times[i]

					prevTotal := prev.User + prev.System + prev.Idle + prev.Nice + prev.Iowait + prev.Irq + prev.Softirq + prev.Steal + prev.Guest + prev.GuestNice
					currTotal := curr.User + curr.System + curr.Idle + curr.Nice + curr.Iowait + curr.Irq + curr.Softirq + curr.Steal + curr.Guest + curr.GuestNice

					total := currTotal - prevTotal
					iowait := curr.Iowait - prev.Iowait

					if total > 0 {
						iowaitDelta += iowait
						totalDelta += total
					}
				}
				if totalDelta > 0 {
					stats.IOWaitPercent = float32((iowaitDelta / totalDelta) * 100.0)
				}
			}
			// Save for next interval
			lastCPUTimes = times
		}
	*/
	stats.IOWaitPercent = 0

	partitions, err := disk.Partitions(true)
	if err != nil {
		return nil, err
	}

	// We care only about these paths, in this exact order
	priority := []string{"/scratch", "/", "/var"}
	seenDev := make(map[string]bool)
	for _, path := range priority {
		// Skip if the path doesn't exist on this system
		if _, err := os.Stat(path); err != nil {
			continue
		}
		p, ok := findBackingPartition(partitions, path)
		if !ok {
			continue // no partition covers this path (unlikely)
		}
		// Skip if same backing device already included (bind mounts, overlay, same partition)
		if seenDev[p.Device] {
			continue
		}
		// Compute usage for the actual path of interest; fall back to mountpoint on error
		usage, err := disk.Usage(path)
		if err != nil {
			usage, err = disk.Usage(p.Mountpoint)
			if err != nil {
				continue
			}
		}
		stats.Disks = append(stats.Disks, DiskUsage{
			DeviceName:   path, // display the path instead of the device
			UsagePercent: float32(usage.UsedPercent),
		})
		seenDev[p.Device] = true
	}

	now := time.Now()
	diskCounters, err := disk.IOCounters()
	if err != nil {
		return nil, err
	}
	// Per-NIC counters so we can exclude loopback and virtual interfaces.
	// `net.IOCounters(false)` aggregates across all interfaces including
	// `lo` and docker/wg/veth/tun bridges — that double-counts container
	// and internal-service traffic and produces wildly inflated numbers
	// (observed: scitq UI showing 37 MB/s "sent" while iftop on the same
	// host showed ~459 Kb/s of real external upload).
	netCounters, err := net.IOCounters(true)
	if err != nil {
		return nil, err
	}

	var readTotal, writeTotal uint64
	for _, v := range diskCounters {
		readTotal += v.ReadBytes
		writeTotal += v.WriteBytes
	}
	stats.DiskIO.ReadBytesTotal = int64(readTotal)
	stats.DiskIO.WriteBytesTotal = int64(writeTotal)

	var nowSent, nowRecv uint64
	for _, c := range netCounters {
		if isVirtualNIC(c.Name) {
			continue
		}
		nowSent += c.BytesSent
		nowRecv += c.BytesRecv
	}
	stats.NetIO.RecvBytesTotal = int64(nowRecv)
	stats.NetIO.SentBytesTotal = int64(nowSent)

	if !lastTime.IsZero() {
		dt := now.Sub(lastTime).Seconds()
		if dt > 0 {
			var lastReadTotal, lastWriteTotal uint64
			for _, v := range lastDiskIO {
				lastReadTotal += v.ReadBytes
				lastWriteTotal += v.WriteBytes
			}
			stats.DiskIO.ReadBytesRate = float32(float64(readTotal-lastReadTotal) / dt)
			stats.DiskIO.WriteBytesRate = float32(float64(writeTotal-lastWriteTotal) / dt)

			if lastNetIO != nil {
				lastTotal, ok := lastNetIO["total"]
				if ok {
					stats.NetIO.RecvBytesRate = float32(float64(nowRecv-lastTotal.BytesRecv) / dt)
					stats.NetIO.SentBytesRate = float32(float64(nowSent-lastTotal.BytesSent) / dt)
				}
			}
		}
	}

	lastDiskIO = diskCounters
	if lastNetIO == nil {
		lastNetIO = make(map[string]net.IOCountersStat)
	}
	// Cache the filtered totals — we don't keep per-interface history,
	// only the cumulative-since-boot non-virtual sum, so deltas line up
	// with what we report.
	lastNetIO["total"] = net.IOCountersStat{BytesSent: nowSent, BytesRecv: nowRecv}
	lastTime = now

	return &stats, nil
}

// ToProto converts WorkerStats to the protobuf pb.WorkerStats
func (ws *WorkerStats) ToProto() *pb.WorkerStats {
	if ws == nil {
		return nil
	}
	var disks []*pb.DiskUsage
	for _, d := range ws.Disks {
		disks = append(disks, &pb.DiskUsage{
			DeviceName:   d.DeviceName,
			UsagePercent: d.UsagePercent,
		})
	}
	return &pb.WorkerStats{
		CpuUsagePercent: ws.CPUUsagePercent,
		MemUsagePercent: ws.MemUsagePercent,
		Load_1Min:       ws.Load1Min,
		IowaitPercent:   ws.IOWaitPercent,
		Disks:           disks,
		DiskIo: &pb.DiskIOStats{
			ReadBytesTotal:  ws.DiskIO.ReadBytesTotal,
			WriteBytesTotal: ws.DiskIO.WriteBytesTotal,
			ReadBytesRate:   ws.DiskIO.ReadBytesRate,
			WriteBytesRate:  ws.DiskIO.WriteBytesRate,
		},
		NetIo: &pb.NetIOStats{
			RecvBytesTotal: ws.NetIO.RecvBytesTotal,
			SentBytesTotal: ws.NetIO.SentBytesTotal,
			RecvBytesRate:  ws.NetIO.RecvBytesRate,
			SentBytesRate:  ws.NetIO.SentBytesRate,
		},
		NumCpus: ws.NumCPUs,
	}
}
