package media

import (
	"testing"
	"time"

	"github.com/pion/rtcp"
)

func TestReceptionTrackerBuildsReceiverReport(t *testing.T) {
	tracker := NewReceptionTracker(8000)
	start := time.Unix(1000, 0)

	tracker.Observe(RTPPacket{SSRC: 99, SequenceNumber: 10, Timestamp: 0}, start)
	tracker.Observe(RTPPacket{SSRC: 99, SequenceNumber: 12, Timestamp: 320}, start.Add(40*time.Millisecond))

	metrics := tracker.Snapshot()
	if metrics.PacketsExpected != 3 || metrics.PacketsReceived != 2 || metrics.PacketsLost != 1 {
		t.Fatalf("unexpected metrics: %#v", metrics)
	}

	rawReport, err := tracker.ReceiverReport(1234)
	if err != nil {
		t.Fatal(err)
	}

	packets, err := rtcp.Unmarshal(rawReport)
	if err != nil {
		t.Fatal(err)
	}

	if len(packets) != 1 {
		t.Fatalf("got %d RTCP packets, want 1", len(packets))
	}

	report, ok := packets[0].(*rtcp.ReceiverReport)
	if !ok || len(report.Reports) != 1 {
		t.Fatalf("unexpected RTCP report: %#v", packets[0])
	}

	if report.SSRC != 1234 || report.Reports[0].SSRC != 99 || report.Reports[0].TotalLost != 1 {
		t.Fatalf("unexpected RTCP fields: %#v", report)
	}
}
