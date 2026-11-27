package backup

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportStageApply(t *testing.T) {
	cfg, data := t.TempDir(), t.TempDir()
	dl, _ := json.Marshal(t.TempDir()) // absolute on every platform
	os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(`{"downloadDir":`+string(dl)+`,"listenPort":6881,"webPort":8899}`), 0o600)
	os.WriteFile(filepath.Join(cfg, "secret.key"), []byte("never exported"), 0o600)
	os.WriteFile(filepath.Join(data, "session.json"), []byte(`{"version":1,"torrents":[]}`), 0o600)
	os.MkdirAll(filepath.Join(data, "torrents"), 0o700)
	h := strings.Repeat("ab", 20)
	os.WriteFile(filepath.Join(data, "torrents", h+".torrent"), []byte("d4:infod4:name1:xee"), 0o600)

	var buf bytes.Buffer
	if err := Export(&buf, cfg, data, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf.Bytes(), []byte("never exported")) {
		t.Fatal("secret key leaked into the backup")
	}

	cfg2, data2 := t.TempDir(), t.TempDir()
	v, n, err := Stage(buf.Bytes(), data2)
	if err != nil || v != "1.2.3" || n != 1 {
		t.Fatalf("stage: %q %d %v", v, n, err)
	}
	ok, err := ApplyPending(cfg2, data2)
	if err != nil || !ok {
		t.Fatalf("apply: %v %v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(data2, "torrents", h+".torrent")); err != nil {
		t.Fatal("torrent not restored")
	}
	if b, _ := os.ReadFile(filepath.Join(cfg2, "settings.json")); !bytes.Contains(b, []byte("6881")) {
		t.Fatal("settings not restored")
	}
	if ok, _ := ApplyPending(cfg2, data2); ok {
		t.Fatal("applied twice")
	}
}

func TestStageRejectsForeignFiles(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("../../evil.sh")
	f.Write([]byte("rm -rf ~"))
	m, _ := zw.Create("magnetar-backup.json")
	m.Write([]byte(`{"app":"Magnetar","version":"1"}`))
	zw.Close()
	data := t.TempDir()
	if _, _, err := Stage(buf.Bytes(), data); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(data), "evil.sh")); err == nil {
		t.Fatal("wrote outside the data folder")
	}
	if _, _, err := Stage([]byte("not a zip"), data); err == nil {
		t.Fatal("garbage accepted")
	}
}
