package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestProjectManagerSessionIndexRead(t *testing.T) {
	first := `{"id":"session-a","thread_name":"renamed a"}`
	second := `{"id":"session-b","thread_name":"已改名 {\"内容\"}\\路径\n下一行"}`
	want := []projectManagerSessionIndexEntry{
		{ID: "session-a", ThreadName: "renamed a"},
		{ID: "session-b", ThreadName: "已改名 {\"内容\"}\\路径\n下一行"},
	}
	tests := []struct {
		name string
		data string
		want []projectManagerSessionIndexEntry
	}{
		{name: "normal JSONL", data: first + "\n" + second + "\n", want: want},
		{name: "concatenated records", data: first + second, want: want},
		{name: "mixed separators", data: " \n" + first + second + "\r\n\t", want: want},
		{name: "BOM and CRLF", data: "\xef\xbb\xbf" + first + "\r\n" + second + "\r\n", want: want},
		{name: "no final newline", data: first + "\n" + second, want: want},
		{name: "empty file", want: []projectManagerSessionIndexEntry{}},
		{name: "BOM only", data: "\xef\xbb\xbf", want: []projectManagerSessionIndexEntry{}},
		{name: "blank file", data: " \r\n\n\t", want: []projectManagerSessionIndexEntry{}},
		{
			name: "invalid timestamp keeps title",
			data: `{"id":" session-a ","thread_name":" renamed a ","updated_at":"invalid"}`,
			want: want[:1],
		},
		{
			name: "timestamp precision",
			data: `{"id":"session-a","thread_name":"renamed a","updated_at":"2026-10-19T01:02:03.123456789Z"}`,
			want: []projectManagerSessionIndexEntry{{ID: "session-a", ThreadName: "renamed a", UpdatedAt: time.Date(2026, 10, 19, 1, 2, 3, 123456789, time.UTC)}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _, path := newProjectManagerSessionIndexTestService(t)
			writeProjectManagerSessionIndexTestFile(t, path, tt.data)
			got, err := service.readProjectManagerSessionIndex()
			if err != nil {
				t.Fatalf("read session index: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("entries = %#v, want %#v", got, tt.want)
			}
			if got := string(readProjectManagerSessionIndexTestFile(t, path)); got != tt.data {
				t.Fatal("reading the session index modified its source")
			}
		})
	}
}

func TestProjectManagerSessionIndexRejectsCorruption(t *testing.T) {
	valid := `{"id":"session-a","thread_name":"original"}`
	for _, invalid := range []string{
		`{"id":`, `{"id":"broken",}`, `null`, `[]`, `"text"`, `{"id":42}`, `{"thread_name":{}}`,
	} {
		t.Run(invalid, func(t *testing.T) {
			service, _, path := newProjectManagerSessionIndexTestService(t)
			data := valid + "\n" + invalid
			writeProjectManagerSessionIndexTestFile(t, path, data)
			if _, err := service.readProjectManagerSessionIndex(); err == nil || !strings.Contains(err.Error(), "字节") {
				t.Fatalf("read error = %v, want corruption with a byte location", err)
			}
			// 整个文件校验成功后才允许写入，不能因目标在坏记录之前就覆盖原文件。
			if err := service.tryRenameCodexSessionIndex("session-a", "new name"); err == nil {
				t.Fatal("rename accepted a corrupt index")
			}
			if err := service.removeCodexSessionIndexEntry("session-a"); err == nil {
				t.Fatal("delete accepted a corrupt index")
			}
			if got := string(readProjectManagerSessionIndexTestFile(t, path)); got != data {
				t.Fatal("a failed operation overwrote the corrupt source")
			}
		})
	}
}

func TestProjectManagerSessionIndexRenameLastRecord(t *testing.T) {
	older := `{"id":"session-a","thread_name":"older","history":true}`
	other := `{"id":"session-b","thread_name":"unchanged","extra":[1,"keep"]}`
	latest := `{"id":"session-a","thread_name":"latest","updated_at":"2026-10-19T01:02:03Z","extra":{"number":900719925474099312345,"escaped":"a\\b"}}`
	newName := "重命名 {\"会话\"}\\目录\n下一行"
	for _, format := range []struct {
		name    string
		bom     string
		newline string
	}{
		{name: "LF", newline: "\n"},
		{name: "CRLF with BOM", bom: "\xef\xbb\xbf", newline: "\r\n"},
	} {
		t.Run(format.name, func(t *testing.T) {
			service, _, path := newProjectManagerSessionIndexTestService(t)
			// 生效记录是粘连行的第二个对象，且同一 ID 在前面还有历史名称。
			writeProjectManagerSessionIndexTestFile(t, path, format.bom+older+format.newline+other+latest)
			if err := service.RenameSession("session-a", newName); err != nil {
				t.Fatalf("rename session: %v", err)
			}
			data := readProjectManagerSessionIndexTestFile(t, path)
			assertProjectManagerSessionIndexTestLines(t, data, format.bom, format.newline, 3)
			document, err := parseProjectManagerSessionIndex(data)
			if err != nil {
				t.Fatalf("parse renamed index: %v", err)
			}
			if string(document.Records[0].Raw) != older || string(document.Records[1].Raw) != other {
				t.Fatal("rename modified a historical or unrelated record")
			}
			if document.Records[2].Entry.ThreadName != newName {
				t.Fatalf("effective name = %q, want %q", document.Records[2].Entry.ThreadName, newName)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(document.Records[2].Raw, &fields); err != nil {
				t.Fatalf("decode renamed fields: %v", err)
			}
			if string(fields["extra"]) != `{"number":900719925474099312345,"escaped":"a\\b"}` || string(fields["updated_at"]) != `"2026-10-19T01:02:03Z"` {
				t.Fatal("rename lost unknown fields, number precision, or the original timestamp")
			}
			appendProjectManagerSessionIndexTestRecord(t, path, format.newline)
			assertProjectManagerSessionIndexTestLines(t, readProjectManagerSessionIndexTestFile(t, path), format.bom, format.newline, 4)
		})
	}
}

func TestProjectManagerSessionIndexDeleteKeepsOtherRecords(t *testing.T) {
	first := `{"id":"keep-a","thread_name":"保留一","extra":900719925474099312345}`
	target := `{"id":"target","thread_name":"delete"}`
	last := `{"id":"keep-b","thread_name":"保留二","extra":{"keep":true}}`
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("newline %q", newline), func(t *testing.T) {
			service, _, path := newProjectManagerSessionIndexTestService(t)
			writeProjectManagerSessionIndexTestFile(t, path, first+target+newline+target+last)
			if err := service.removeCodexSessionIndexEntry("target"); err != nil {
				t.Fatalf("remove session index records: %v", err)
			}
			data := readProjectManagerSessionIndexTestFile(t, path)
			if string(data) != first+newline+last+newline {
				t.Fatalf("remaining records = %s, want both other sessions and a final newline", data)
			}
			appendProjectManagerSessionIndexTestRecord(t, path, newline)
			assertProjectManagerSessionIndexTestLines(t, readProjectManagerSessionIndexTestFile(t, path), "", newline, 3)
		})
	}
}

func TestProjectManagerSessionIndexMissingAndEmpty(t *testing.T) {
	service, _, path := newProjectManagerSessionIndexTestService(t)
	if entries, err := service.readProjectManagerSessionIndex(); err != nil || len(entries) != 0 {
		t.Fatalf("read missing index = %#v, %v", entries, err)
	}
	if err := service.removeCodexSessionIndexEntry("missing"); err != nil {
		t.Fatalf("delete from missing index: %v", err)
	}
	if err := service.tryRenameCodexSessionIndex("missing", "name"); err == nil {
		t.Fatal("rename accepted a missing index")
	}
	data := `{"id":"target","thread_name":"original"}`
	writeProjectManagerSessionIndexTestFile(t, path, data)
	if err := service.removeCodexSessionIndexEntry("missing"); err != nil {
		t.Fatalf("delete missing ID: %v", err)
	}
	if err := service.tryRenameCodexSessionIndex("missing", "name"); err == nil {
		t.Fatal("rename accepted a missing ID")
	}
	if string(readProjectManagerSessionIndexTestFile(t, path)) != data {
		t.Fatal("an operation on a missing ID rewrote the source")
	}
	for _, bom := range []string{"", "\xef\xbb\xbf"} {
		writeProjectManagerSessionIndexTestFile(t, path, bom+data)
		if err := service.removeCodexSessionIndexEntry("target"); err != nil {
			t.Fatalf("delete final record: %v", err)
		}
		if string(readProjectManagerSessionIndexTestFile(t, path)) != bom {
			t.Fatal("deleting the final record left stale content or changed the BOM")
		}
	}
}

func TestProjectManagerSessionIndexSnapshotRebuildsOldCache(t *testing.T) {
	service, home, path := newProjectManagerSessionIndexTestService(t)
	for _, id := range []string{"session-a", "session-b", "session-c", "session-d"} {
		writeProjectManagerSessionIndexTestHistory(t, home, id)
	}
	data := `{"id":"session-a","thread_name":"older"}` + "\n" +
		`{"id":"session-a","thread_name":"改名后的会话 A"}{"id":"session-b","thread_name":"改名后的会话 B"}` + "\n" +
		`{"id":"session-c","thread_name":"Codex title C"}`
	writeProjectManagerSessionIndexTestFile(t, path, data)
	if err := service.store.saveSessionMetadata("session-c", func(meta *projectManagerSessionMeta) bool {
		meta.DisplayName = "local alias C"
		return true
	}); err != nil {
		t.Fatalf("seed local alias: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat index: %v", err)
	}
	cache := newProjectManagerSnapshotCache()
	cache.Version = 4
	cache.SessionIndexMeta = projectManagerFileSignature(info)
	cache.Snapshot.Sessions = []SessionSummary{{ID: "session-a", DisplayName: "session-a"}}
	cache.Snapshot.SnapshotUpdatedAt = time.Now().UnixMilli()
	if err := service.snapshotCache.save(cache); err != nil {
		t.Fatalf("seed old snapshot cache: %v", err)
	}

	// 从卡片实际读取的快照接口验收，确保文件签名没变时也不会复用旧版漏读结果。
	snapshot, err := service.GetSnapshot()
	if err != nil {
		t.Fatalf("get rebuilt snapshot: %v", err)
	}
	want := map[string]string{
		"session-a": "改名后的会话 A", "session-b": "改名后的会话 B",
		"session-c": "local alias C", "session-d": "session-d",
	}
	got := make(map[string]string, len(snapshot.Sessions))
	for _, session := range snapshot.Sessions {
		got[session.ID] = session.DisplayName
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("card display names = %#v, want %#v", got, want)
	}
	cache, err = service.snapshotCache.load()
	if err != nil || cache.Version != 5 || len(cache.SessionIndexEntries) != 4 {
		t.Fatalf("rebuilt cache version = %d, entries = %d, error = %v", cache.Version, len(cache.SessionIndexEntries), err)
	}
	if string(readProjectManagerSessionIndexTestFile(t, path)) != data {
		t.Fatal("snapshot rebuilding modified the source index")
	}
}

func TestProjectManagerRenameSessionInvalidatesCachesOnPartialSuccess(t *testing.T) {
	for _, scenario := range []string{"success", "missing index", "missing ID", "corrupt index"} {
		t.Run(scenario, func(t *testing.T) {
			service, home, path := newProjectManagerSessionIndexTestService(t)
			writeProjectManagerSessionIndexTestHistory(t, home, "session-a")
			data := ""
			switch scenario {
			case "success":
				data = `{"id":"session-a","thread_name":"old title"}`
			case "missing ID":
				data = `{"id":"session-b","thread_name":"other"}`
			case "corrupt index":
				data = `{"id":"session-a","thread_name":"old title"}{"id":`
			}
			if scenario != "missing index" {
				writeProjectManagerSessionIndexTestFile(t, path, data)
			}
			cache := newProjectManagerSnapshotCache()
			cache.Snapshot.Sessions = []SessionSummary{{ID: "session-a", DisplayName: "old title"}}
			cache.Snapshot.SnapshotUpdatedAt = time.Now().UnixMilli()
			if err := service.snapshotCache.save(cache); err != nil {
				t.Fatalf("seed snapshot cache: %v", err)
			}
			service.detailCache.save("session-a", projectManagerConversationCacheEntry{})
			service.conversationSearchCache.save("session-a", projectManagerConversationSearchCacheEntry{})

			err := service.RenameSession("session-a", "new local alias")
			if scenario == "success" && err != nil {
				t.Fatalf("rename session: %v", err)
			}
			if scenario != "success" && (err == nil || !strings.Contains(err.Error(), "已保存本地会话别名")) {
				t.Fatalf("partial rename error = %v", err)
			}
			store, err := service.store.load()
			if err != nil || store.Sessions["session-a"].DisplayName != "new local alias" {
				t.Fatalf("saved local alias = %#v, %v", store.Sessions["session-a"], err)
			}
			if _, err := os.Stat(service.snapshotCache.path); !os.IsNotExist(err) {
				t.Fatalf("snapshot cache remains after rename: %v", err)
			}
			if _, ok := service.detailCache.load("session-a"); ok {
				t.Fatal("conversation detail cache remains after rename")
			}
			if _, ok := service.conversationSearchCache.load("session-a", projectManagerTrackedFile{}); ok {
				t.Fatal("conversation search cache remains after rename")
			}
			if scenario == "corrupt index" {
				if string(readProjectManagerSessionIndexTestFile(t, path)) != data {
					t.Fatal("partial rename overwrote the corrupt index")
				}
				return
			}
			snapshot, err := service.GetSnapshot()
			if err != nil || len(snapshot.Sessions) != 1 || snapshot.Sessions[0].DisplayName != "new local alias" {
				t.Fatalf("snapshot after rename = %#v, %v", snapshot.Sessions, err)
			}
		})
	}
}

func newProjectManagerSessionIndexTestService(t *testing.T) (*ProjectManagerService, string, string) {
	t.Helper()
	// 所有读写只指向临时家目录，避免测试接触本机真实的 Codex 索引与缓存。
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return NewProjectManagerService(), home, filepath.Join(home, ".codex", "session_index.jsonl")
}

func writeProjectManagerSessionIndexTestFile(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func readProjectManagerSessionIndexTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func writeProjectManagerSessionIndexTestHistory(t *testing.T, home string, sessionID string) {
	t.Helper()
	data := fmt.Sprintf(`{"type":"session_meta","timestamp":"2026-10-19T00:00:00Z","payload":{"id":%q,"cwd":%q}}`, sessionID, filepath.Join(home, "workspace"))
	writeProjectManagerSessionIndexTestFile(t, filepath.Join(home, ".codex", "sessions", sessionID+".jsonl"), data+"\n")
}

func appendProjectManagerSessionIndexTestRecord(t *testing.T, path string, newline string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open index for appending: %v", err)
	}
	defer file.Close()
	if _, err := file.WriteString(`{"id":"appended","thread_name":"new session"}` + newline); err != nil {
		t.Fatalf("append index record: %v", err)
	}
}

func assertProjectManagerSessionIndexTestLines(t *testing.T, data []byte, bom string, newline string, count int) {
	t.Helper()
	if bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) != (bom != "") {
		t.Fatal("index BOM changed")
	}
	text := strings.TrimPrefix(string(data), bom)
	if !strings.HasSuffix(text, newline) || strings.Count(text, "\n") != count || (newline == "\r\n" && strings.Count(text, "\r\n") != count) {
		t.Fatalf("index record count or line separators changed: %q", text)
	}
	for _, line := range strings.Split(strings.TrimSuffix(text, newline), newline) {
		if !json.Valid([]byte(line)) || !strings.HasPrefix(line, "{") {
			t.Fatalf("index line is not one complete JSON object: %q", line)
		}
	}
}
