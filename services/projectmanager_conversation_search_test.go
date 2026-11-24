package services

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestProjectManagerConversationSearchMatchesMessages(t *testing.T) {
	tests := []struct {
		name     string
		contents []string
		query    string
		want     []string
	}{
		{
			name:     "all matching messages newest first",
			contents: []string{"old needle", "unrelated", "middle NEEDLE needle", "new needle"},
			query:    "needle",
			want:     []string{"new needle", "middle NEEDLE needle", "old needle"},
		},
		{
			name:     "identical messages remain separate matches",
			contents: []string{"needle", "needle"},
			query:    "needle",
			want:     []string{"needle", "needle"},
		},
		{
			name:     "Chinese repeated keyword counts once per message",
			contents: []string{"旧消息关键词", "新消息关键词和关键词"},
			query:    "关键词",
			want:     []string{"新消息关键词和关键词", "旧消息关键词"},
		},
		{
			name:     "no match returns an empty array",
			contents: []string{"unrelated"},
			query:    "needle",
			want:     []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			documents := make([]projectManagerConversationSearchDocument, 0, len(tt.contents))
			for _, content := range tt.contents {
				documents = append(documents, projectManagerConversationSearchDocument{Content: content})
			}
			got := matchProjectManagerConversationSearchDocuments(documents, tt.query, strings.ToLower(tt.query))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("matches = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSearchProjectSessionConversationsReturnsAllUserMatches(t *testing.T) {
	dir := t.TempDir()
	projectPath := filepath.Join(dir, "project-a")
	conversationPath := filepath.Join(dir, "session-a.jsonl")
	metadataPath := filepath.Join(dir, "metadata.jsonl")
	lines := []string{
		`{"type":"event_msg","payload":{"type":"user_message","message":"old needle"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"agent-only needle"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"middle NEEDLE needle"}]}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"新消息关键词和关键词"}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"new needle"}}`,
	}
	if err := os.WriteFile(conversationPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatalf("write conversation fixture: %v", err)
	}
	if err := os.WriteFile(metadataPath, []byte(`{"type":"event_msg","payload":{"type":"user_message","message":"unrelated"}}`), 0o600); err != nil {
		t.Fatalf("write metadata fixture: %v", err)
	}

	cache := newProjectManagerSnapshotCache()
	// 测试通过临时快照进入真实接口，关闭暖刷新，避免读取或修改本机的历史会话。
	cache.Snapshot.Sessions = []SessionSummary{
		{ID: "session-a", ProjectPath: projectPath, DisplayName: "Conversation"},
		{ID: "metadata", ProjectPath: projectPath, DisplayName: "Needle metadata"},
		{ID: "other-project", ProjectPath: filepath.Join(dir, "project-b"), DisplayName: "Needle elsewhere"},
	}
	cache.SessionFiles[conversationPath] = projectManagerCachedSessionFile{
		Entry: projectManagerSessionFileEntry{SessionID: "session-a", Path: conversationPath},
	}
	cache.SessionFiles[metadataPath] = projectManagerCachedSessionFile{
		Entry: projectManagerSessionFileEntry{SessionID: "metadata", Path: metadataPath},
	}
	service := &ProjectManagerService{
		snapshotCache:           &projectManagerSnapshotCacheService{path: filepath.Join(dir, "snapshot.json")},
		conversationSearchCache: newProjectManagerConversationSearchCacheService(),
	}
	if err := service.snapshotCache.save(cache); err != nil {
		t.Fatalf("save snapshot fixture: %v", err)
	}

	results, err := service.SearchProjectSessionConversations(projectPath, "  NeEdLe  ")
	if err != nil {
		t.Fatalf("search conversations: %v", err)
	}
	if len(results) != 2 || results[0].SessionID != "session-a" || results[1].SessionID != "metadata" {
		t.Fatalf("search results = %#v, want only the matching project sessions", results)
	}
	want := []string{"new needle", "middle NEEDLE needle", "old needle"}
	if !reflect.DeepEqual(results[0].MatchedContents, want) {
		t.Fatalf("conversation matches = %#v, want %#v", results[0].MatchedContents, want)
	}
	if results[1].MatchedContents == nil || len(results[1].MatchedContents) != 0 {
		t.Fatalf("metadata matches = %#v, want a non-nil empty array", results[1].MatchedContents)
	}
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("encode results: %v", err)
	}
	if !strings.Contains(string(encoded), `"matched_contents":[]`) || strings.Contains(string(encoded), `"matched_content":`) {
		t.Fatalf("unexpected search response contract: %s", encoded)
	}

	chineseResults, err := service.SearchProjectSessionConversations(projectPath, "关键词")
	if err != nil || len(chineseResults) != 1 || len(chineseResults[0].MatchedContents) != 1 {
		t.Fatalf("Chinese search = %#v, error = %v, want one matching user message", chineseResults, err)
	}
	agentOnly, err := service.SearchProjectSessionConversations(projectPath, "agent-only")
	if err != nil || len(agentOnly) != 0 {
		t.Fatalf("agent-only search = %#v, error = %v, want no matches", agentOnly, err)
	}

	// 修改文件长度确保缓存签名变化，验证重新搜索读取新增消息而非沿用旧摘要列表。
	lines = append(lines, `{"type":"event_msg","payload":{"type":"user_message","message":"latest needle"}}`)
	if err := os.WriteFile(conversationPath, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatalf("update conversation fixture: %v", err)
	}
	refreshed, err := service.SearchProjectSessionConversations(projectPath, "needle")
	if err != nil || len(refreshed) != 2 || len(refreshed[0].MatchedContents) != 4 || refreshed[0].MatchedContents[0] != "latest needle" {
		t.Fatalf("refreshed search = %#v, error = %v, want the newly added match first", refreshed, err)
	}

	// 已索引源文件不可读时继续快速失败，不能把不完整结果伪装成成功搜索。
	if err := os.Remove(conversationPath); err != nil {
		t.Fatalf("remove conversation fixture: %v", err)
	}
	failed, err := service.SearchProjectSessionConversations(projectPath, "needle")
	if err == nil || failed != nil || !strings.Contains(err.Error(), "session-a") {
		t.Fatalf("unreadable source search = %#v, error = %v, want a session-specific error", failed, err)
	}
}

func TestProjectManagerConversationSearchExcerptPreservesKeyword(t *testing.T) {
	for _, query := range []string{"关键词", strings.Repeat("长", 150)} {
		content := strings.Repeat("前", 200) + query + strings.Repeat("后", 200)
		excerpt := buildProjectManagerConversationSearchExcerpt(content, query)
		if !utf8.ValidString(excerpt) || !strings.Contains(excerpt, query) {
			t.Fatalf("excerpt does not preserve the UTF-8 keyword: %q", excerpt)
		}
		limit := projectManagerSearchExcerptRunes
		// 长关键词必须完整保留，摘要允许包含其前面的定位上下文，不强行截断关键词。
		if keywordWithContext := utf8.RuneCountInString(query) + projectManagerSearchExcerptPrefixRunes; keywordWithContext > limit {
			limit = keywordWithContext
		}
		if utf8.RuneCountInString(excerpt) > limit+6 {
			t.Fatalf("excerpt has %d runes, want at most %d", utf8.RuneCountInString(excerpt), limit+6)
		}
	}
}
