package history

import (
	"path/filepath"
	"sort"

	"gpumon/internal/recording"
)

// Pending 為某記錄檔尚未寫入磁碟的記錄, 依寫入的先後排列, 時刻不遞減. 查詢時視為接續於該檔的檔尾.
//
// 取樣記錄的 GPUs 須與該檔說明區的 GPU 一一對應, 順序相同; 錯誤記錄與斷線記錄以一筆 Record 表示,
// Text 為完整的文字, 不另列接續記錄.
type Pending struct {
	Path    string
	Records []recording.Record
}

// recordSource 為查詢所讀取的記錄來源, 由 *recording.File 或接續了尚未存檔記錄的檔案提供.
type recordSource interface {
	Close() error
	NumGPUs() int
	GPUIDs() []int
	Count() (int, error)
	TimeRange() (first, last int64, ok bool, err error)
	LowerBound(t int64) (int, error)
	ReadIndexRange(start, end int) ([]recording.Record, error)
}

func pendingFor(pending []Pending, path string) []recording.Record {
	clean := filepath.Clean(path)
	var out []recording.Record
	for _, p := range pending {
		if filepath.Clean(p.Path) == clean {
			out = append(out, p.Records...)
		}
	}
	return out
}

// pendingSource 為檔案內前 base 筆記錄之後接續 recs 的記錄來源. 索引 base 起對應 recs 的元素.
type pendingSource struct {
	f    *recording.File
	base int
	recs []recording.Record
}

func newPendingSource(f *recording.File, recs []recording.Record) (*pendingSource, error) {
	n, err := f.Count()
	if err != nil {
		return nil, err
	}
	return &pendingSource{f: f, base: n, recs: recs}, nil
}

func (s *pendingSource) Close() error  { return s.f.Close() }
func (s *pendingSource) NumGPUs() int  { return s.f.NumGPUs() }
func (s *pendingSource) GPUIDs() []int { return s.f.GPUIDs() }

func (s *pendingSource) Count() (int, error) { return s.base + len(s.recs), nil }

func (s *pendingSource) TimeRange() (first, last int64, ok bool, err error) {
	if s.base > 0 {
		if first, last, ok, err = s.f.TimeRange(); err != nil {
			return 0, 0, false, err
		}
	}
	if len(s.recs) == 0 {
		return first, last, ok, nil
	}
	if !ok {
		first = s.recs[0].Time
	}
	return first, s.recs[len(s.recs)-1].Time, true, nil
}

func (s *pendingSource) LowerBound(t int64) (int, error) {
	if s.base > 0 {
		i, err := s.f.LowerBound(t)
		if err != nil {
			return 0, err
		}
		if i < s.base {
			return i, nil
		}
	}
	return s.base + sort.Search(len(s.recs), func(j int) bool { return s.recs[j].Time >= t }), nil
}

func (s *pendingSource) ReadIndexRange(start, end int) ([]recording.Record, error) {
	start = max(start, 0)
	end = min(end, s.base+len(s.recs))
	if start >= end {
		return nil, nil
	}
	var out []recording.Record
	if start < s.base {
		recs, err := s.f.ReadIndexRange(start, min(end, s.base))
		if err != nil {
			return nil, err
		}
		out = recs
	}
	if end > s.base {
		out = append(out, s.recs[max(start-s.base, 0):end-s.base]...)
	}
	return out, nil
}
