package db

import "github.com/uptrace/bun"

type DirectoryModel struct {
	bun.BaseModel `bun:"table:directories"`

	ID   int64  `bun:",pk,autoincrement"`
	Path string `bun:",notnull,unique"`
}

type EntryModel struct {
	bun.BaseModel `bun:"table:entries"`

	ID        int64  `bun:",pk,autoincrement"`
	Name      string `bun:",notnull"`
	DirID     int64  `bun:"dir_id,notnull"`
	Ext       string `bun:",notnull"`
	Size      int64  `bun:",notnull"`
	MTime     int64  `bun:"mtime,notnull"`
	IsDir     bool   `bun:"is_dir,notnull"`
	Root      string `bun:",notnull"`
	IndexedAt int64  `bun:"indexed_at,notnull"`
}
