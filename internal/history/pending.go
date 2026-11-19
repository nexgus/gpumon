package history

import (
	"path/filepath"

	"gpumon/internal/recording"
)

// Pending 為某記錄檔尚未寫入磁碟的記錄, 依寫入的先後排列, 時刻不遞減. 查詢時視為接續於該檔的檔尾.
//
// 取樣記錄的 GPUs 須與該檔說明區的 GPU 一一對應, 順序相同; 錯誤記錄與斷線記錄的 Text 為完整的文字.
type Pending struct {
	Path    string
	Records []recording.Record
}

// recordSource 為查詢所讀取的記錄來源, 由記錄檔或接續了尚未存檔記錄的記錄檔提供.
type recordSource interface {
	Close() error
	NumGPUs() int
	GPUIDs() []int
	TimeRange() (first, last int64, ok bool, err error)
	// SeekTime 的意義與 recording.File.SeekTime 相同.
	SeekTime(t int64, withProcs bool) (segments, error)
}

// segments 依序提供記錄, 意義與 recording.Iter 相同.
type segments interface {
	Next() ([]recording.Record, bool, error)
	Prev() ([]recording.Record, bool, error)
}

// fileSource 以記錄檔本身作為記錄來源.
type fileSource struct{ *recording.File }

func (s fileSource) SeekTime(t int64, withProcs bool) (segments, error) {
	return s.File.SeekTime(t, withProcs)
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

// pendingSource 為記錄檔之後接續 recs 的記錄來源. recs 視為檔尾之後的最後一段.
type pendingSource struct {
	f    *recording.File
	recs []recording.Record
}

func newPendingSource(f *recording.File, recs []recording.Record) *pendingSource {
	return &pendingSource{f: f, recs: recs}
}

func (s *pendingSource) Close() error  { return s.f.Close() }
func (s *pendingSource) NumGPUs() int  { return s.f.NumGPUs() }
func (s *pendingSource) GPUIDs() []int { return s.f.GPUIDs() }

func (s *pendingSource) TimeRange() (first, last int64, ok bool, err error) {
	if first, last, ok, err = s.f.TimeRange(); err != nil {
		return 0, 0, false, err
	}
	if len(s.recs) == 0 {
		return first, last, ok, nil
	}
	if !ok {
		first = s.recs[0].Time
	}
	return first, s.recs[len(s.recs)-1].Time, true, nil
}

func (s *pendingSource) SeekTime(t int64, withProcs bool) (segments, error) {
	it, err := s.f.SeekTime(t, withProcs)
	if err != nil {
		return nil, err
	}
	return &pendingSegments{it: it, recs: s.recs}, nil
}

// pendingSegments 往後讀完記錄檔後, 以 recs 作為最後一段; 往前只讀記錄檔.
type pendingSegments struct {
	it       *recording.Iter
	recs     []recording.Record
	fileDone bool
	recsDone bool
}

func (p *pendingSegments) Next() ([]recording.Record, bool, error) {
	if !p.fileDone {
		recs, ok, err := p.it.Next()
		if err != nil || ok {
			return recs, ok, err
		}
		p.fileDone = true
	}
	if p.recsDone || len(p.recs) == 0 {
		return nil, false, nil
	}
	p.recsDone = true
	return p.recs, true, nil
}

func (p *pendingSegments) Prev() ([]recording.Record, bool, error) { return p.it.Prev() }
