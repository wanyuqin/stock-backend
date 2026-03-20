package service

import (
	"sort"
	"sync"
	"sync/atomic"
)

type fallbackCounter struct {
	module string
	from   string
	to     string
	count  atomic.Int64
}

var dataSourceFallbackCounters sync.Map // key: module|from|to -> *fallbackCounter

func recordDataSourceFallback(module, from, to string) {
	key := module + "|" + from + "|" + to
	if v, ok := dataSourceFallbackCounters.Load(key); ok {
		v.(*fallbackCounter).count.Add(1)
		return
	}
	c := &fallbackCounter{module: module, from: from, to: to}
	c.count.Store(1)
	actual, _ := dataSourceFallbackCounters.LoadOrStore(key, c)
	if actual != c {
		actual.(*fallbackCounter).count.Add(1)
	}
}

type DataSourceFallbackStat struct {
	Module string `json:"module"`
	From   string `json:"from"`
	To     string `json:"to"`
	Count  int64  `json:"count"`
}

type DataSourceStatus struct {
	DefaultSource string                   `json:"default_source"`
	Fallbacks     []DataSourceFallbackStat `json:"fallbacks"`
}

func BuildDataSourceStatus(defaultSource string) *DataSourceStatus {
	stats := make([]DataSourceFallbackStat, 0, 16)
	dataSourceFallbackCounters.Range(func(_, value any) bool {
		c := value.(*fallbackCounter)
		stats = append(stats, DataSourceFallbackStat{
			Module: c.module,
			From:   c.from,
			To:     c.to,
			Count:  c.count.Load(),
		})
		return true
	})
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Module != stats[j].Module {
			return stats[i].Module < stats[j].Module
		}
		if stats[i].From != stats[j].From {
			return stats[i].From < stats[j].From
		}
		return stats[i].To < stats[j].To
	})
	return &DataSourceStatus{
		DefaultSource: normalizeMarketSource(defaultSource),
		Fallbacks:     stats,
	}
}
