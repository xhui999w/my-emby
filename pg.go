package main

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/lib/pq"
)

// pgPersister 使用 PostgreSQL 持久化。表结构在启动时自动创建。
type pgPersister struct {
	db *sql.DB
}

const pgSchema = `
CREATE TABLE IF NOT EXISTS app_meta (
	k TEXT PRIMARY KEY,
	v TEXT
);
CREATE TABLE IF NOT EXISTS users (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	pass_hash TEXT DEFAULT '',
	salt TEXT DEFAULT '',
	is_admin BOOLEAN DEFAULT FALSE,
	has_password BOOLEAN DEFAULT FALSE,
	playback_disabled BOOLEAN DEFAULT FALSE,
	download_disabled BOOLEAN DEFAULT FALSE,
	max_devices INTEGER DEFAULT 0
);
ALTER TABLE users ADD COLUMN IF NOT EXISTS playback_disabled BOOLEAN DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS download_disabled BOOLEAN DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS max_devices INTEGER DEFAULT 0;
CREATE TABLE IF NOT EXISTS auth_tokens (
	token TEXT PRIMARY KEY,
	user_id TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS libraries (
	id TEXT PRIMARY KEY,
	name TEXT DEFAULT '',
	collection_type TEXT DEFAULT 'movies',
	paths JSONB DEFAULT '[]'
);
CREATE TABLE IF NOT EXISTS media_items (
	id TEXT PRIMARY KEY,
	data JSONB NOT NULL
);
CREATE TABLE IF NOT EXISTS activity_log (
	id TEXT PRIMARY KEY,
	name TEXT DEFAULT '',
	overview TEXT DEFAULT '',
	type TEXT DEFAULT '',
	severity TEXT DEFAULT '',
	created_at TEXT DEFAULT ''
);
CREATE TABLE IF NOT EXISTS media_display_names (
	id TEXT PRIMARY KEY,
	name TEXT DEFAULT ''
);
CREATE TABLE IF NOT EXISTS settings (
	k TEXT PRIMARY KEY,
	v TEXT DEFAULT ''
);
`

func newPGPersister(dsn string) (*pgPersister, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(pgSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &pgPersister{db: db}, nil
}

func (p *pgPersister) Close() error { return p.db.Close() }

func (p *pgPersister) Load() (*dbSnapshot, error) {
	snap := &dbSnapshot{Tokens: map[string]string{}}

	_ = p.db.QueryRow(`SELECT v FROM app_meta WHERE k='server_id'`).Scan(&snap.ServerId)

	if rows, err := p.db.Query(`SELECT id,name,pass_hash,salt,is_admin,has_password,playback_disabled,download_disabled,max_devices FROM users`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var u dbUser
			if err := rows.Scan(&u.Id, &u.Name, &u.PassHash, &u.Salt, &u.IsAdmin, &u.HasPassword, &u.PlaybackDisabled, &u.DownloadDisabled, &u.MaxDevices); err == nil {
				snap.Users = append(snap.Users, u)
			}
		}
	}

	if rows, err := p.db.Query(`SELECT token,user_id FROM auth_tokens`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var t, uid string
			if err := rows.Scan(&t, &uid); err == nil {
				snap.Tokens[t] = uid
			}
		}
	}

	if rows, err := p.db.Query(`SELECT id,name,collection_type,paths FROM libraries`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, name, ct string
			var raw []byte
			if err := rows.Scan(&id, &name, &ct, &raw); err == nil {
				var paths []string
				_ = json.Unmarshal(raw, &paths)
				snap.Libraries = append(snap.Libraries, &Library{Id: id, Name: name, CollectionType: ct, Paths: paths})
			}
		}
	}

	if rows, err := p.db.Query(`SELECT id,name,overview,type,severity,created_at FROM activity_log ORDER BY created_at DESC LIMIT 500`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var a Activity
			if err := rows.Scan(&a.Id, &a.Name, &a.ShortOverview, &a.Type, &a.Severity, &a.Date); err == nil {
				snap.Activity = append(snap.Activity, a)
			}
		}
	}

	if rows, err := p.db.Query(`SELECT data FROM media_items`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err == nil {
				var rec itemRecord
				if json.Unmarshal(raw, &rec) == nil {
					snap.Items = append(snap.Items, rec)
				}
			}
		}
	}

	if rows, err := p.db.Query(`SELECT id,name FROM media_display_names`); err == nil {
		defer rows.Close()
		snap.DisplayNames = map[string]string{}
		for rows.Next() {
			var id, name string
			if err := rows.Scan(&id, &name); err == nil {
				snap.DisplayNames[id] = name
			}
		}
	}

	if rows, err := p.db.Query(`SELECT k,v FROM settings`); err == nil {
		defer rows.Close()
		snap.Settings = map[string]string{}
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err == nil {
				snap.Settings[k] = v
			}
		}
	}

	return snap, nil
}

func (p *pgPersister) Save(snap *dbSnapshot) error {
	tx, err := p.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`INSERT INTO app_meta(k,v) VALUES('server_id',$1)
		ON CONFLICT (k) DO UPDATE SET v=EXCLUDED.v`, snap.ServerId); err != nil {
		return err
	}

	// users：upsert + 删除不存在的
	userIDs := make([]string, 0, len(snap.Users))
	for _, u := range snap.Users {
		userIDs = append(userIDs, u.Id)
		if _, err := tx.Exec(`INSERT INTO users(id,name,pass_hash,salt,is_admin,has_password,playback_disabled,download_disabled,max_devices)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name,pass_hash=EXCLUDED.pass_hash,
			salt=EXCLUDED.salt,is_admin=EXCLUDED.is_admin,has_password=EXCLUDED.has_password,
			playback_disabled=EXCLUDED.playback_disabled,download_disabled=EXCLUDED.download_disabled,max_devices=EXCLUDED.max_devices`,
			u.Id, u.Name, u.PassHash, u.Salt, u.IsAdmin, u.HasPassword,
			u.PlaybackDisabled, u.DownloadDisabled, u.MaxDevices); err != nil {
			return err
		}
	}
	if len(userIDs) > 0 {
		if _, err := tx.Exec(`DELETE FROM users WHERE NOT (id = ANY($1))`, pq.Array(userIDs)); err != nil {
			return err
		}
	}

	// tokens：整体替换
	if _, err := tx.Exec(`DELETE FROM auth_tokens`); err != nil {
		return err
	}
	for tok, uid := range snap.Tokens {
		if _, err := tx.Exec(`INSERT INTO auth_tokens(token,user_id) VALUES($1,$2)
			ON CONFLICT (token) DO UPDATE SET user_id=EXCLUDED.user_id`, tok, uid); err != nil {
			return err
		}
	}

	// libraries：upsert + 删除不存在的
	libIDs := make([]string, 0, len(snap.Libraries))
	for _, l := range snap.Libraries {
		libIDs = append(libIDs, l.Id)
		paths, _ := json.Marshal(l.Paths)
		if _, err := tx.Exec(`INSERT INTO libraries(id,name,collection_type,paths) VALUES($1,$2,$3,$4)
			ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name,collection_type=EXCLUDED.collection_type,paths=EXCLUDED.paths`,
			l.Id, l.Name, l.CollectionType, string(paths)); err != nil {
			return err
		}
	}
	if len(libIDs) > 0 {
		if _, err := tx.Exec(`DELETE FROM libraries WHERE NOT (id = ANY($1))`, pq.Array(libIDs)); err != nil {
			return err
		}
	}

	// activity：整体替换
	if _, err := tx.Exec(`DELETE FROM activity_log`); err != nil {
		return err
	}
	for _, a := range snap.Activity {
		if _, err := tx.Exec(`INSERT INTO activity_log(id,name,overview,type,severity,created_at)
			VALUES($1,$2,$3,$4,$5,$6)`, a.Id, a.Name, a.ShortOverview, a.Type, a.Severity, a.Date); err != nil {
			return err
		}
	}

	// media_display_names：整体替换
	if _, err := tx.Exec(`DELETE FROM media_display_names`); err != nil {
		return err
	}
	for id, name := range snap.DisplayNames {
		if _, err := tx.Exec(`INSERT INTO media_display_names(id,name) VALUES($1,$2)
			ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name`, id, name); err != nil {
			return err
		}
	}

	// settings：整体替换
	if _, err := tx.Exec(`DELETE FROM settings`); err != nil {
		return err
	}
	for k, v := range snap.Settings {
		if _, err := tx.Exec(`INSERT INTO settings(k,v) VALUES($1,$2)
			ON CONFLICT (k) DO UPDATE SET v=EXCLUDED.v`, k, v); err != nil {
			return err
		}
	}

	// media_items：upsert + 删除不存在的
	itemIDs := make([]string, 0, len(snap.Items))
	for _, rec := range snap.Items {
		itemIDs = append(itemIDs, rec.Id)
		raw, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO media_items(id,data) VALUES($1,$2)
			ON CONFLICT (id) DO UPDATE SET data=EXCLUDED.data`, rec.Id, string(raw)); err != nil {
			return err
		}
	}
	if len(itemIDs) > 0 {
		if _, err := tx.Exec(`DELETE FROM media_items WHERE NOT (id = ANY($1))`, pq.Array(itemIDs)); err != nil {
			return err
		}
	}

	return tx.Commit()
}
