package qqmusic

import "testing"

func TestVersionPolicy_BuildComm_Web(t *testing.T) {
	p := DefaultVersionPolicy()
	c := &Credential{MusicID: 100, MusicKey: "key"}
	comm := p.BuildComm(PlatformWeb, c, nil, nil, "guid")
	if comm["ct"] != 24 {
		t.Errorf("ct: %v", comm["ct"])
	}
	if comm["platform"] != "yqq.json" {
		t.Errorf("platform: %v", comm["platform"])
	}
	if comm["g_tk"] != comm["g_tk_new_20200303"] {
		t.Errorf("g_tk pair mismatch")
	}
	if comm["format"] != "json" {
		t.Errorf("format: %v", comm["format"])
	}
}

func TestVersionPolicy_BuildComm_Android(t *testing.T) {
	p := DefaultVersionPolicy()
	c := &Credential{MusicID: 100, MusicKey: "K", LoginType: 2}
	dev := &Device{AndroidID: "aid", Model: "Pixel", Fingerprint: "fp", OSRelease: "11", OSSDK: 30}
	comm := p.BuildComm(PlatformAndroid, c, dev, &QimeiPair{Q16: "16", Q36: "36"}, "guid")

	mustEq := func(k string, want any) {
		if comm[k] != want {
			t.Errorf("%s: got %v want %v", k, comm[k], want)
		}
	}
	mustEq("ct", 11)
	mustEq("cv", 14090008)
	mustEq("v", 14090008)
	mustEq("chid", "10003505")
	mustEq("tmeAppID", "qqmusic")
	mustEq("tmeLoginType", 2)
	mustEq("QIMEI", "16")
	mustEq("QIMEI36", "36")
	mustEq("authst", "K")
	mustEq("phonetype", "Pixel")
	mustEq("os_ver", "11")
}

func TestVersionPolicy_GetGTK(t *testing.T) {
	p := DefaultVersionPolicy()
	if got := p.GetGTK(&Credential{}); got != 5381 {
		t.Errorf("empty key g_tk: %d", got)
	}
	if got := p.GetGTK(&Credential{MusicKey: "abc"}); got == 5381 {
		t.Errorf("non-empty key should differ from 5381, got %d", got)
	}
}

func TestVersionPolicy_UserAgent(t *testing.T) {
	p := DefaultVersionPolicy()
	dev := &Device{OSRelease: "13"}
	if ua := p.GetUserAgent(PlatformAndroid, dev); ua != "QQMusic 14090008(android 13)" {
		t.Errorf("android UA: %q", ua)
	}
	if ua := p.GetUserAgent(PlatformWeb, nil); ua == "" {
		t.Errorf("web UA empty")
	}
}
