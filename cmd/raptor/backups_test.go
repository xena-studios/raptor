package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
)

var testBackups = []*localv1.BackupInfo{
	{
		Id: "01a0f000-0000-7000-8000-0000000b0001", ServerId: "0190a1b2-0000-7000-8000-00000000aaaa", ServerName: "survival",
		Kind: "manual", Status: "ok", Locked: true, SizeBytes: 3 << 30, UploadedBytes: 20 << 20, DestinationId: "local",
		CreatedAt: timestamppb.New(time.Date(2026, 3, 2, 4, 0, 0, 0, time.Local)), Warning: "the server didn't confirm the save",
	},
	{
		Id: "01a0f000-0000-7000-8000-0000000b0002", ServerId: "0190a1b2-0000-7000-8000-00000000dead",
		Kind: "scheduled", Status: "failed", Error: "disk full", DestinationId: "b2",
		CreatedAt: timestamppb.New(time.Date(2026, 3, 1, 4, 0, 0, 0, time.Local)),
	},
}

func TestConfirmRestore(t *testing.T) {
	for _, tc := range []struct {
		name, ref, input string
		tty              bool
		ok               bool
	}{
		{"typed the name", "0000b0001", "survival\n", true, true},
		{"full ID", "01a0f000-0000-7000-8000-0000000b0001", "survival", true, true},
		{"wrong name", "0000b0001", "creative\n", true, false},
		{"nothing typed", "0000b0001", "", true, false},
		{"no terminal", "0000b0001", "survival\n", false, false},
		{"unknown backup", "ffffffff", "survival\n", true, false},
	} {
		err := confirmRestore(testBackups, tc.ref, strings.NewReader(tc.input), io.Discard, tc.tty)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}

func TestPrintBackups(t *testing.T) {
	var out bytes.Buffer
	if err := printBackups(&out, testBackups, true); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{
		"000b0001", "survival", "2026-03-02 04:00", "3.0 GiB", "20.0 MiB", "locked", "warning: the server didn't confirm",
		"0000dead (deleted)", "failed", "disk full",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}
