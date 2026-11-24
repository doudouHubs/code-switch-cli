package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tidwall/sjson"
)

type projectManagerSessionIndexEntry struct {
	ID         string    `json:"id"`
	ThreadName string    `json:"thread_name"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type projectManagerSessionIndexRecord struct {
	Raw   json.RawMessage
	Entry projectManagerSessionIndexEntry
}

type projectManagerSessionIndexDocument struct {
	Records []projectManagerSessionIndexRecord
	HasBOM  bool
	Newline string
}

func (s *ProjectManagerService) readProjectManagerSessionIndex() ([]projectManagerSessionIndexEntry, error) {
	document, _, err := readProjectManagerSessionIndexDocument()
	if err != nil {
		if os.IsNotExist(err) {
			return []projectManagerSessionIndexEntry{}, nil
		}
		return nil, err
	}

	entries := make([]projectManagerSessionIndexEntry, 0, len(document.Records))
	for _, record := range document.Records {
		entries = append(entries, record.Entry)
	}
	return entries, nil
}

func (s *ProjectManagerService) tryRenameCodexSessionIndex(sessionID string, displayName string) error {
	document, path, err := readProjectManagerSessionIndexDocument()
	if err != nil {
		return err
	}

	// 读取端以最后一条记录为准；改名必须修改同一条，避免旧记录盖过新名称。
	for index := len(document.Records) - 1; index >= 0; index-- {
		if document.Records[index].Entry.ID != sessionID {
			continue
		}
		raw, err := sjson.SetBytes(document.Records[index].Raw, "thread_name", displayName)
		if err != nil {
			return err
		}
		document.Records[index].Raw = raw
		return document.write(path)
	}
	return fmt.Errorf("session_index.jsonl 中未找到会话 %s", sessionID)
}

func (s *ProjectManagerService) removeCodexSessionIndexEntry(sessionID string) error {
	document, path, err := readProjectManagerSessionIndexDocument()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	kept := make([]projectManagerSessionIndexRecord, 0, len(document.Records))
	for _, record := range document.Records {
		// 删除全部历史记录，且只按对象过滤，不能连带删除粘在同一行的其它会话。
		if record.Entry.ID != sessionID {
			kept = append(kept, record)
		}
	}
	if len(kept) == len(document.Records) {
		return nil
	}
	document.Records = kept
	if err := document.write(path); err != nil {
		return err
	}
	s.invalidateProjectManagerSnapshotCache()
	return nil
}

func readProjectManagerSessionIndexDocument() (projectManagerSessionIndexDocument, string, error) {
	home, err := getUserHomeDir()
	if err != nil {
		return projectManagerSessionIndexDocument{}, "", err
	}
	path := filepath.Join(home, ".codex", "session_index.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return projectManagerSessionIndexDocument{}, path, err
	}
	document, err := parseProjectManagerSessionIndex(data)
	if err != nil {
		return projectManagerSessionIndexDocument{}, path, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	return document, path, nil
}

func parseProjectManagerSessionIndex(data []byte) (projectManagerSessionIndexDocument, error) {
	document := projectManagerSessionIndexDocument{
		Records: make([]projectManagerSessionIndexRecord, 0, 256),
		HasBOM:  bytes.HasPrefix(data, []byte("\xef\xbb\xbf")),
		Newline: "\n",
	}
	if index := bytes.IndexByte(data, '\n'); index > 0 && data[index-1] == '\r' {
		document.Newline = "\r\n"
	}
	if document.HasBOM {
		data = data[3:]
	}

	// Codex 追加前的索引可能没有末尾换行；按对象流解析才能读回粘连记录中的名称。
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var raw json.RawMessage
		offset := decoder.InputOffset()
		if err := decoder.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return projectManagerSessionIndexDocument{}, fmt.Errorf("第 %d 条记录（字节 %d 附近）: %w", len(document.Records)+1, offset, err)
		}
		if len(raw) == 0 || raw[0] != '{' {
			return projectManagerSessionIndexDocument{}, fmt.Errorf("第 %d 条记录（字节 %d 附近）必须为 JSON 对象", len(document.Records)+1, offset)
		}
		var entry struct {
			ID         string `json:"id"`
			ThreadName string `json:"thread_name"`
			UpdatedAt  string `json:"updated_at"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			return projectManagerSessionIndexDocument{}, fmt.Errorf("第 %d 条记录（字节 %d 附近）: %w", len(document.Records)+1, offset, err)
		}
		// 时间格式异常沿用零值处理，不能因此丢掉仍有效的会话名称。
		updatedAt, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.UpdatedAt))
		document.Records = append(document.Records, projectManagerSessionIndexRecord{
			Raw: raw,
			Entry: projectManagerSessionIndexEntry{
				ID:         strings.TrimSpace(entry.ID),
				ThreadName: strings.TrimSpace(entry.ThreadName),
				UpdatedAt:  updatedAt,
			},
		})
	}
	return document, nil
}

func (document projectManagerSessionIndexDocument) write(path string) error {
	var output bytes.Buffer
	if document.HasBOM {
		output.WriteString("\xef\xbb\xbf")
	}
	for _, record := range document.Records {
		// 保留未知字段和数值原文；压成一行后补分隔符，防止后续追加再次粘连。
		if err := json.Compact(&output, record.Raw); err != nil {
			return fmt.Errorf("编码 session_index.jsonl 记录失败: %w", err)
		}
		output.WriteString(document.Newline)
	}
	return AtomicWriteBytes(path, output.Bytes())
}
