package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// KnowledgeDoc represents a single markdown document in the knowledge base.
type KnowledgeDoc struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Content    string    `json:"content,omitempty"`
	SourceName string    `json:"sourceName,omitempty"`
	Category   string    `json:"category"`
	CreatedBy  string    `json:"createdBy,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// KnowledgeCategory is a grouping label.
type KnowledgeCategory struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func newKnowledgeID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return "kd-" + hex.EncodeToString(buf)
}

// ListKnowledgeCategories returns all distinct categories with doc counts.
func ListKnowledgeCategories() []KnowledgeCategory {
	current := currentStore()
	if current == nil {
		return []KnowledgeCategory{}
	}
	rows, err := current.Query(`SELECT COALESCE(NULLIF(category,''),'基础') AS c, COUNT(*) FROM knowledge_docs GROUP BY c ORDER BY c`)
	if err != nil {
		return []KnowledgeCategory{}
	}
	defer rows.Close()
	items := []KnowledgeCategory{}
	for rows.Next() {
		var item KnowledgeCategory
		if rows.Scan(&item.Name, &item.Count) == nil {
			items = append(items, item)
		}
	}
	return items
}

// ListKnowledgeDocs returns metadata for all docs (without content).
func ListKnowledgeDocs(keyword string, category string) []KnowledgeDoc {
	current := currentStore()
	if current == nil {
		return []KnowledgeDoc{}
	}
	query := `SELECT id, title, COALESCE(source_name,''), COALESCE(NULLIF(category,''),'基础'), COALESCE(created_by,''), created_at, updated_at FROM knowledge_docs WHERE 1=1`
	args := []any{}
	if strings.TrimSpace(keyword) != "" {
		query += ` AND title LIKE ?`
		args = append(args, "%"+strings.TrimSpace(keyword)+"%")
	}
	if strings.TrimSpace(category) != "" && category != "全部" {
		query += ` AND COALESCE(NULLIF(category,''),'基础') = ?`
		args = append(args, strings.TrimSpace(category))
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := current.Query(query, args...)
	if err != nil {
		return []KnowledgeDoc{}
	}
	defer rows.Close()
	items := []KnowledgeDoc{}
	for rows.Next() {
		var item KnowledgeDoc
		if rows.Scan(&item.ID, &item.Title, &item.SourceName, &item.Category, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt) == nil {
			items = append(items, item)
		}
	}
	return items
}

// GetKnowledgeDoc returns a single doc with full content.
func GetKnowledgeDoc(id string) (*KnowledgeDoc, error) {
	current := currentStore()
	if current == nil {
		return nil, errors.New("数据库未初始化")
	}
	var item KnowledgeDoc
	err := current.QueryRow(`SELECT id, title, content, COALESCE(source_name,''), COALESCE(NULLIF(category,''),'基础'), COALESCE(created_by,''), created_at, updated_at FROM knowledge_docs WHERE id = ?`, id).
		Scan(&item.ID, &item.Title, &item.Content, &item.SourceName, &item.Category, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return nil, errors.New("文档不存在")
	}
	return &item, nil
}

// SaveKnowledgeDoc creates or updates a doc.
func SaveKnowledgeDoc(doc *KnowledgeDoc) (*KnowledgeDoc, error) {
	current := currentStore()
	if current == nil {
		return nil, errors.New("数据库未初始化")
	}
	title := strings.TrimSpace(doc.Title)
	if title == "" {
		return nil, errors.New("标题不能为空")
	}
	category := strings.TrimSpace(doc.Category)
	if category == "" {
		category = "基础"
	}
	if doc.ID == "" {
		doc.ID = newKnowledgeID()
		_, err := current.Exec(`INSERT INTO knowledge_docs (id, title, content, source_name, category, created_by) VALUES (?, ?, ?, ?, ?, ?)`,
			doc.ID, title, doc.Content, doc.SourceName, category, doc.CreatedBy)
		if err != nil {
			return nil, fmt.Errorf("保存失败: %w", err)
		}
	} else {
		_, err := current.Exec(`UPDATE knowledge_docs SET title=?, content=?, source_name=?, category=? WHERE id=?`,
			title, doc.Content, doc.SourceName, category, doc.ID)
		if err != nil {
			return nil, fmt.Errorf("更新失败: %w", err)
		}
	}
	return GetKnowledgeDoc(doc.ID)
}

// DeleteKnowledgeDoc removes a doc.
func DeleteKnowledgeDoc(id string) error {
	current := currentStore()
	if current == nil {
		return errors.New("数据库未初始化")
	}
	_, err := current.Exec(`DELETE FROM knowledge_docs WHERE id=?`, id)
	return err
}

// ImportKnowledgeDoc imports a single markdown file as a new doc.
func ImportKnowledgeDoc(filename string, content string, createdBy string) (*KnowledgeDoc, error) {
	title := strings.TrimSuffix(strings.TrimSpace(filename), ".md")
	title = strings.TrimSuffix(title, ".markdown")
	if title == "" {
		title = "未命名文档"
	}
	doc := &KnowledgeDoc{
		Title:      title,
		Content:    content,
		SourceName: filename,
		Category:   "基础",
		CreatedBy:  createdBy,
	}
	return SaveKnowledgeDoc(doc)
}

// AllKnowledgeDocsForExport returns all docs with content for bulk export.
func AllKnowledgeDocsForExport() []KnowledgeDoc {
	current := currentStore()
	if current == nil {
		return []KnowledgeDoc{}
	}
	rows, err := current.Query(`SELECT id, title, content, COALESCE(source_name,''), COALESCE(NULLIF(category,''),'基础'), created_at, updated_at FROM knowledge_docs ORDER BY updated_at DESC`)
	if err != nil {
		return []KnowledgeDoc{}
	}
	defer rows.Close()
	items := []KnowledgeDoc{}
	for rows.Next() {
		var item KnowledgeDoc
		if rows.Scan(&item.ID, &item.Title, &item.Content, &item.SourceName, &item.Category, &item.CreatedAt, &item.UpdatedAt) == nil {
			items = append(items, item)
		}
	}
	return items
}
