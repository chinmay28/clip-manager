package clips

import (
	"testing"
	"time"
)

func TestChannelFromName(t *testing.T) {
	cases := []struct {
		name, want string
	}{
		// The Dahua/Amcrest FTP naming this app was built around.
		{"N843A8_ch3_main_20260830214636_20260830214641.dav", "N843A8_ch3"},
		{"N843A8_ch12_sub_20260830214636.mp4", "N843A8_ch12"},
		// No device prefix — the channel token alone still identifies.
		{"ch4_20260830214636.dav", "ch4"},
		{"CH4-main.avi", "ch4"},
		// Nothing that reads as a channel.
		{"12.00.01.mp4", ""},
		{"driveway-clip.mp4", ""},
		// "ch" buried in a word is not a channel token.
		{"porch3.mp4", ""},
	}
	for _, c := range cases {
		if got := channelFromName(c.name); got != c.want {
			t.Errorf("channelFromName(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestStartTime(t *testing.T) {
	mod := time.Date(2026, 8, 31, 4, 0, 0, 0, time.Local)

	// A Dahua name carries start and end stamps; the first is the start.
	got := startTime(
		"2026-08-30/N843A8_ch3_main_20260830214636_20260830214641.dav",
		"N843A8_ch3_main_20260830214636_20260830214641.dav", mod)
	want := time.Date(2026, 8, 30, 21, 46, 36, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("dahua name: got %v, want %v", got, want)
	}

	// The date-directory layout: date in the path, time in the name.
	got = startTime("front-door/2026-08-30/12.00.01.mp4", "12.00.01.mp4", mod)
	want = time.Date(2026, 8, 30, 12, 0, 1, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("date-dir layout: got %v, want %v", got, want)
	}

	// A name saying nothing falls back to the file's mod time.
	got = startTime("cam/whatever.mp4", "whatever.mp4", mod)
	if !got.Equal(mod) {
		t.Errorf("fallback: got %v, want mod time %v", got, mod)
	}

	// A 14-digit run that is not a real date (month 13) is not a timestamp.
	got = startTime("cam/x.mp4", "x_20261340214636.mp4", mod)
	if !got.Equal(mod) {
		t.Errorf("bad stamp: got %v, want mod time %v", got, mod)
	}
}
