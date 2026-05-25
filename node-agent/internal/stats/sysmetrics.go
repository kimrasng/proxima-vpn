package stats

import (
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
)

type SysMetrics struct {
	CPU        float64
	Memory     float64
	Disk       float64
	LoadAvg    float64
	NetworkIn  float64
	NetworkOut float64
}

func CollectSysMetrics() SysMetrics {
	var m SysMetrics

	if percents, err := cpu.Percent(0, false); err == nil && len(percents) > 0 {
		m.CPU = percents[0]
	}

	if vmStat, err := mem.VirtualMemory(); err == nil {
		m.Memory = vmStat.UsedPercent
	}

	if diskStat, err := disk.Usage("/"); err == nil {
		m.Disk = diskStat.UsedPercent
	}

	if loadStat, err := load.Avg(); err == nil {
		m.LoadAvg = loadStat.Load1
	}

	if netStats, err := net.IOCounters(false); err == nil && len(netStats) > 0 {
		m.NetworkIn = float64(netStats[0].BytesRecv)
		m.NetworkOut = float64(netStats[0].BytesSent)
	}

	return m
}
