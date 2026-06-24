package service

import (
	"sort"
	"sync"
	"time"
)

// DataShareExportDurationPartKey 标识预生成导出链路中的一个耗时阶段。
type DataShareExportDurationPartKey string

const (
	DataShareExportDurationPartCount         DataShareExportDurationPartKey = "export_count"
	DataShareExportDurationPartDBPage        DataShareExportDurationPartKey = "export_db_page"
	DataShareExportDurationPartPayloadDecode DataShareExportDurationPartKey = "export_payload_decode"
	DataShareExportDurationPartRedactRecheck DataShareExportDurationPartKey = "export_redact_recheck"
	DataShareExportDurationPartJSONMarshal   DataShareExportDurationPartKey = "export_json_marshal"
	DataShareExportDurationPartWriteCompress DataShareExportDurationPartKey = "export_write_compress"
	DataShareExportDurationPartGenerateTotal DataShareExportDurationPartKey = "export_generate_total"
)

// DataShareExportDurationRecorder 接收预生成导出链路耗时样本。
type DataShareExportDurationRecorder interface {
	Observe(part DataShareExportDurationPartKey, duration time.Duration)
}

// DataShareExportDurationObserveFunc 允许 repository 通过回调上报导出内部阶段耗时。
type DataShareExportDurationObserveFunc func(part DataShareExportDurationPartKey, duration time.Duration)

// Observe 实现 DataShareExportDurationRecorder，便于函数直接作为 recorder 使用。
func (f DataShareExportDurationObserveFunc) Observe(part DataShareExportDurationPartKey, duration time.Duration) {
	if f == nil {
		return
	}
	f(part, duration)
}

// DataShareExportDurationStats 是管理端可见的预生成导出耗时统计快照。
type DataShareExportDurationStats struct {
	WindowSize  int                           `json:"window_size"`
	SampleCount int                           `json:"sample_count"`
	Parts       []DataShareExportDurationPart `json:"parts"`
}

// DataShareExportDurationPart 是单个导出阶段的耗时统计。
type DataShareExportDurationPart struct {
	Key         string                          `json:"key"`
	Label       string                          `json:"label"`
	Category    string                          `json:"category"`
	LastMillis  int64                           `json:"last_millis"`
	AvgMillis   float64                         `json:"avg_millis"`
	P50Millis   int64                           `json:"p50_millis"`
	P95Millis   int64                           `json:"p95_millis"`
	MaxMillis   int64                           `json:"max_millis"`
	SampleCount int                             `json:"sample_count"`
	Buckets     []DataShareExportDurationBucket `json:"buckets"`
}

// DataShareExportDurationBucket 是固定耗时区间的样本数量。
type DataShareExportDurationBucket struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type dataShareExportDurationPartDefinition struct {
	key      DataShareExportDurationPartKey
	label    string
	category string
}

var dataShareExportDurationPartDefinitions = []dataShareExportDurationPartDefinition{
	{key: DataShareExportDurationPartCount, label: "总数统计", category: "准备"},
	{key: DataShareExportDurationPartDBPage, label: "游标分页", category: "读取"},
	{key: DataShareExportDurationPartPayloadDecode, label: "Payload 解码", category: "读取"},
	{key: DataShareExportDurationPartRedactRecheck, label: "脱敏复核", category: "生成"},
	{key: DataShareExportDurationPartJSONMarshal, label: "JSON 序列化", category: "生成"},
	{key: DataShareExportDurationPartWriteCompress, label: "写入压缩", category: "输出"},
	{key: DataShareExportDurationPartGenerateTotal, label: "生成总耗时", category: "总览"},
}

// dataShareExportDurationRecorder 在进程内按阶段保存最近 N 个导出耗时样本。
type dataShareExportDurationRecorder struct {
	mu         sync.RWMutex
	windowSize int
	parts      map[DataShareExportDurationPartKey]*dataShareCaptureDurationRing
}

func newDataShareExportDurationRecorder(windowSize int) *dataShareExportDurationRecorder {
	recorder := &dataShareExportDurationRecorder{
		windowSize: normalizeDataShareCaptureDurationWindowSize(windowSize),
		parts:      map[DataShareExportDurationPartKey]*dataShareCaptureDurationRing{},
	}
	for _, def := range dataShareExportDurationPartDefinitions {
		recorder.parts[def.key] = newDataShareCaptureDurationRing(recorder.windowSize)
	}
	return recorder
}

func (r *dataShareExportDurationRecorder) Observe(part DataShareExportDurationPartKey, duration time.Duration) {
	if r == nil || part == "" {
		return
	}
	millis := duration.Milliseconds()
	if millis < 0 {
		millis = 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ring := r.parts[part]
	if ring == nil {
		ring = newDataShareCaptureDurationRing(r.windowSize)
		r.parts[part] = ring
	}
	ring.add(millis)
}

func (r *dataShareExportDurationRecorder) SetWindowSize(windowSize int) {
	if r == nil {
		return
	}
	windowSize = normalizeDataShareCaptureDurationWindowSize(windowSize)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.windowSize == windowSize {
		return
	}
	r.windowSize = windowSize
	for _, ring := range r.parts {
		ring.resize(windowSize)
	}
}

func (r *dataShareExportDurationRecorder) Snapshot() DataShareExportDurationStats {
	if r == nil {
		return DataShareExportDurationStats{WindowSize: defaultDataSharingCaptureDurationWindowSize}
	}
	r.mu.RLock()
	windowSize := r.windowSize
	samplesByPart := make(map[DataShareExportDurationPartKey][]int64, len(r.parts))
	for key, ring := range r.parts {
		samplesByPart[key] = ring.samples()
	}
	r.mu.RUnlock()

	stats := DataShareExportDurationStats{
		WindowSize: windowSize,
		Parts:      make([]DataShareExportDurationPart, 0, len(dataShareExportDurationPartDefinitions)),
	}
	for _, def := range dataShareExportDurationPartDefinitions {
		part := buildDataShareExportDurationPart(def, samplesByPart[def.key])
		if part.SampleCount > stats.SampleCount {
			stats.SampleCount = part.SampleCount
		}
		stats.Parts = append(stats.Parts, part)
	}
	return stats
}

func buildDataShareExportDurationPart(def dataShareExportDurationPartDefinition, samples []int64) DataShareExportDurationPart {
	part := DataShareExportDurationPart{
		Key:      string(def.key),
		Label:    def.label,
		Category: def.category,
		Buckets:  buildDataShareExportDurationBuckets(samples),
	}
	if len(samples) == 0 {
		return part
	}
	part.SampleCount = len(samples)
	part.LastMillis = samples[len(samples)-1]
	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var total int64
	for _, value := range samples {
		total += value
		if value > part.MaxMillis {
			part.MaxMillis = value
		}
	}
	part.AvgMillis = float64(total) / float64(len(samples))
	part.P50Millis = percentileFromSortedMillis(sorted, 0.50)
	part.P95Millis = percentileFromSortedMillis(sorted, 0.95)
	return part
}

func buildDataShareExportDurationBuckets(samples []int64) []DataShareExportDurationBucket {
	buckets := make([]DataShareExportDurationBucket, len(dataShareCaptureDurationBucketDefinitions))
	for i, def := range dataShareCaptureDurationBucketDefinitions {
		buckets[i] = DataShareExportDurationBucket{Label: def.label}
	}
	for _, sample := range samples {
		for i, def := range dataShareCaptureDurationBucketDefinitions {
			if def.upper < 0 || sample < def.upper {
				buckets[i].Count++
				break
			}
		}
	}
	return buckets
}
