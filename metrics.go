package main

import (
	"math"
	"runtime"
	"runtime/metrics"
	"time"
)

// goroutines 当前协程数。
func goroutines() int { return runtime.NumGoroutine() }

// memMiB 进程从操作系统获得的内存（近似容器/进程内存）。
func (a *App) memMiB() float64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.Sys) / (1024 * 1024)
}

var cpuMetric = pickCPUMetric()

func pickCPUMetric() string {
	// 优先 user（真实 CPU 消耗，排除 idle），退回 total
	var total string
	for _, d := range metrics.All() {
		if d.Name == "/cpu/classes/user:cpu-seconds" {
			return d.Name
		}
		if d.Name == "/cpu/classes/total:cpu-seconds" {
			total = d.Name
		}
	}
	return total
}

func readCPUSeconds() float64 {
	if cpuMetric == "" {
		return 0
	}
	s := []metrics.Sample{{Name: cpuMetric}}
	metrics.Read(s)
	if s[0].Value.Kind() == metrics.KindFloat64 {
		return s[0].Value.Float64()
	}
	return 0
}

// cpuPercent 进程 CPU 使用率（%）。Go 的 /cpu/classes 指标仅在 Linux 可用，
// 其它平台返回 -1 表示 N/A。
func (a *App) cpuPercent() float64 {
	if runtime.GOOS != "linux" {
		return -1
	}
	now := time.Now()
	cs := readCPUSeconds()
	a.cpuMu.Lock()
	defer a.cpuMu.Unlock()
	if a.cpuLastAt.IsZero() || now.Sub(a.cpuLastAt) <= 0 {
		a.cpuLastAt = now
		a.cpuLast = cs
		return 0
	}
	d := cs - a.cpuLast
	wall := now.Sub(a.cpuLastAt).Seconds()
	a.cpuLast = cs
	a.cpuLastAt = now
	if cpuMetric == "" || wall <= 0 {
		return 0
	}
	p := d / wall * 100
	if p < 0 || math.IsNaN(p) || math.IsInf(p, 0) {
		return 0
	}
	return p
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
