package workerstats

import (
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"

	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
)

// WorkerStats matches your proto definition
// adjust the field tags if you use protobuf-generated structs directly

type WorkerStats struct {
	CPUUsagePercent float32
	MemUsagePercent float32
	Load1Min        float32
	Disks           []DiskUsage
	DiskIO          DiskIOStats
	NetIO           NetIOStats
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
	lastDiskIO map[string]disk.IOCountersStat
	lastNetIO  map[string]net.IOCountersStat
	lastTime   time.Time
)

func CollectWorkerStats() (*WorkerStats, error) {
	var stats WorkerStats
	var err error

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

	partitions, err := disk.Partitions(true)
	if err != nil {
		return nil, err
	}
	for _, p := range partitions {
		usage, err := disk.Usage(p.Mountpoint)
		if err != nil {
			continue
		}
		stats.Disks = append(stats.Disks, DiskUsage{
			DeviceName:   p.Device,
			UsagePercent: float32(usage.UsedPercent),
		})
	}

	now := time.Now()
	diskCounters, err := disk.IOCounters()
	if err != nil {
		return nil, err
	}
	netCounters, err := net.IOCounters(false)
	if err != nil {
		return nil, err
	}

	if !lastTime.IsZero() {
		dt := now.Sub(lastTime).Seconds()
		if dt > 0 {
			// Disk IO aggregation
			var readTotal, writeTotal uint64
			var lastReadTotal, lastWriteTotal uint64
			for _, v := range diskCounters {
				readTotal += v.ReadBytes
				writeTotal += v.WriteBytes
			}
			for _, v := range lastDiskIO {
				lastReadTotal += v.ReadBytes
				lastWriteTotal += v.WriteBytes
			}
			stats.DiskIO.ReadBytesTotal = int64(readTotal)
			stats.DiskIO.WriteBytesTotal = int64(writeTotal)
			stats.DiskIO.ReadBytesRate = float32(readTotal-lastReadTotal) / float32(dt)
			stats.DiskIO.WriteBytesRate = float32(writeTotal-lastWriteTotal) / float32(dt)

			// Net IO aggregation
			if len(netCounters) > 0 {
				nowNet := netCounters[0]
				lastNet := lastNetIO["total"]
				stats.NetIO.RecvBytesTotal = int64(nowNet.BytesRecv)
				stats.NetIO.SentBytesTotal = int64(nowNet.BytesSent)
				stats.NetIO.RecvBytesRate = float32(nowNet.BytesRecv-lastNet.BytesRecv) / float32(dt)
				stats.NetIO.SentBytesRate = float32(nowNet.BytesSent-lastNet.BytesSent) / float32(dt)
			}
		}
	}

	// Save for next time
	lastDiskIO = diskCounters
	if len(netCounters) > 0 {
		if lastNetIO == nil {
			lastNetIO = make(map[string]net.IOCountersStat)
		}
		lastNetIO["total"] = netCounters[0]
	}
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
	}
}
