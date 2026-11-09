package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestDevicesEmptyGPUs(t *testing.T) {
	got := mustJSON(t, Devices{Type: TypeDevices, Protocol: Version, Version: "0.1.0", Hostname: "h", GPUs: []Device{}})
	want := `{"type":"devices","protocol":1,"version":"0.1.0","hostname":"h","gpus":[]}`
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestDeviceOmitsMissingFields(t *testing.T) {
	clock := 1650
	got := mustJSON(t, Device{ID: 2, Vendor: VendorIntel, Name: "x", ClockGPUMax: &clock, PCIVendor: "8086"})
	want := `{"id":2,"vendor":"intel","name":"x","clock_gpu_max":1650,"pci_vendor":"8086","pci_device":"","pci_subsys_vendor":"","pci_subsys_device":""}`
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestSampleProcsNilVersusEmpty(t *testing.T) {
	zero := 0
	s := Sample{Type: TypeSample, TS: 1730700000.123, GPUs: []GPUSample{
		{ID: 0, Util: &zero},
		{ID: 1, Procs: []Process{}},
	}}
	got := mustJSON(t, s)
	want := `{"type":"sample","ts":1730700000.123,"gpus":[{"id":0,"util":0},{"id":1,"procs":[]}]}`
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestProcessRawCounters(t *testing.T) {
	mem := uint64(4194304)
	p := Process{PID: 7, User: "alice", Mem: &mem, Cmd: "glxgears",
		Engines: map[string]uint64{"render": 10, "copy": 0}, EngineCapacity: map[string]uint64{"video": 2}}
	got := mustJSON(t, p)
	want := `{"pid":7,"user":"alice","mem":4194304,"cmd":"glxgears","engines":{"copy":0,"render":10},"engine_capacity":{"video":2}}`
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestTimestamp(t *testing.T) {
	cases := map[Timestamp]string{
		1730700000.123: "1730700000.123",
		1730700000:     "1730700000.000",
		1730700000.1:   "1730700000.100",
	}
	for ts, want := range cases {
		if got := mustJSON(t, ts); got != want {
			t.Errorf("Timestamp(%v) = %s, want %s", float64(ts), got, want)
		}
	}
	tm := time.UnixMilli(1730700000123)
	if got := NewTimestamp(tm); got.Time() != tm {
		t.Errorf("round trip: got %v, want %v", got.Time(), tm)
	}
	var back Error
	if err := json.Unmarshal([]byte(`{"type":"error","ts":1730700000.123,"msg":"m"}`), &back); err != nil {
		t.Fatal(err)
	}
	if back.TS.Time() != tm {
		t.Errorf("decoded ts = %v, want %v", back.TS.Time(), tm)
	}
}

func TestAckAndErrorAndPong(t *testing.T) {
	ms := 500
	cases := []struct {
		v    any
		want string
	}{
		{Ack{Type: TypeAck, Cmd: CmdSetInterval, Ms: &ms}, `{"type":"ack","cmd":"set_interval","ms":500}`},
		{Ack{Type: TypeAck, Cmd: CmdSetFields, Fields: []string{}}, `{"type":"ack","cmd":"set_fields","fields":[]}`},
		{Error{Type: TypeError, TS: 1730700000.123, Msg: "nvml timeout"}, `{"type":"error","ts":1730700000.123,"msg":"nvml timeout"}`},
		{Pong{Type: TypePong}, `{"type":"pong"}`},
	}
	for _, c := range cases {
		if got := mustJSON(t, c.v); got != c.want {
			t.Errorf("got %s\nwant %s", got, c.want)
		}
	}
}

func TestCommandEncoding(t *testing.T) {
	ms := 500
	cases := []struct {
		c    Command
		want string
	}{
		{Command{Cmd: CmdSetInterval, Ms: &ms}, `{"cmd":"set_interval","ms":500}`},
		{Command{Cmd: CmdSetInterval}, `{"cmd":"set_interval"}`},
		{Command{Cmd: CmdSetFields, Fields: []string{"util", "mem_used"}}, `{"cmd":"set_fields","fields":["util","mem_used"]}`},
		{Command{Cmd: CmdSetFields}, `{"cmd":"set_fields"}`},
		{Command{Cmd: CmdSampleNow}, `{"cmd":"sample_now"}`},
		{Command{Cmd: CmdPing}, `{"cmd":"ping"}`},
	}
	for _, c := range cases {
		if got := mustJSON(t, c.c); got != c.want {
			t.Errorf("got %s\nwant %s", got, c.want)
		}
	}
}

func TestMessageTypeIgnoresUnknownFields(t *testing.T) {
	typ, err := MessageType([]byte(`{"type":"sample","future":true,"ts":1.5,"gpus":[]}`))
	if err != nil || typ != TypeSample {
		t.Fatalf("MessageType = %q, %v", typ, err)
	}
	if _, err := MessageType([]byte(`not json`)); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestSampleFields(t *testing.T) {
	if strings.Join(SampleFields, ",") != "util,mem_used,temp,power_w,clock_gpu,clock_mem,fan,enc,dec,procs" {
		t.Errorf("unexpected SampleFields %v", SampleFields)
	}
	if !IsSampleField("procs") || IsSampleField("id") || IsSampleField("bogus") {
		t.Error("IsSampleField mismatch")
	}
	if !ValidInterval(200) || !ValidInterval(10000) || ValidInterval(199) || ValidInterval(10001) {
		t.Error("ValidInterval bounds mismatch")
	}
}
