package main

// ユーザー（users + themes + アイコン）のメモリキャッシュ。
//
// users / themes は登録後に変わらない。アイコンだけ POST /api/icon で更新される。
// DB が正で、起動時と POST /api/initialize で DB から作り直す（再起動試験対策）。
// 更新は「DB にコミットしてからキャッシュに反映」する（コミット前に見せない）。

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"sync"

	"github.com/jmoiron/sqlx"
)

type cachedUser struct {
	User     UserModel
	Theme    ThemeModel
	Image    []byte // nil ならフォールバック画像
	IconHash string
}

type userCache struct {
	mu     sync.RWMutex
	byID   map[int64]*cachedUser
	byName map[string]*cachedUser
}

var (
	users             = &userCache{byID: map[int64]*cachedUser{}, byName: map[string]*cachedUser{}}
	fallbackImageData []byte
	fallbackIconHash  string
)

func loadFallbackImage() error {
	b, err := os.ReadFile(fallbackImage)
	if err != nil {
		return err
	}
	fallbackImageData = b
	fallbackIconHash = fmt.Sprintf("%x", sha256.Sum256(b))
	return nil
}

// DB から全ユーザーを読み直す
func (uc *userCache) reload(ctx context.Context, db *sqlx.DB) error {
	var userModels []UserModel
	if err := db.SelectContext(ctx, &userModels, "SELECT * FROM users"); err != nil {
		return err
	}
	var themes []ThemeModel
	if err := db.SelectContext(ctx, &themes, "SELECT * FROM themes"); err != nil {
		return err
	}
	type iconRow struct {
		UserID int64  `db:"user_id"`
		Image  []byte `db:"image"`
	}
	var icons []iconRow
	if err := db.SelectContext(ctx, &icons, "SELECT user_id, image FROM icons"); err != nil {
		return err
	}

	byID := make(map[int64]*cachedUser, len(userModels))
	byName := make(map[string]*cachedUser, len(userModels))
	for _, u := range userModels {
		cu := &cachedUser{User: u, IconHash: fallbackIconHash}
		byID[u.ID] = cu
		byName[u.Name] = cu
	}
	for _, t := range themes {
		if cu, ok := byID[t.UserID]; ok {
			cu.Theme = t
		}
	}
	for _, ic := range icons {
		if cu, ok := byID[ic.UserID]; ok {
			cu.Image = ic.Image
			cu.IconHash = fmt.Sprintf("%x", sha256.Sum256(ic.Image))
		}
	}

	uc.mu.Lock()
	uc.byID = byID
	uc.byName = byName
	uc.mu.Unlock()
	return nil
}

func (uc *userCache) getByID(id int64) (*cachedUser, bool) {
	uc.mu.RLock()
	defer uc.mu.RUnlock()
	cu, ok := uc.byID[id]
	return cu, ok
}

func (uc *userCache) getByName(name string) (*cachedUser, bool) {
	uc.mu.RLock()
	defer uc.mu.RUnlock()
	cu, ok := uc.byName[name]
	return cu, ok
}

// 登録（DB コミット後に呼ぶ）
func (uc *userCache) add(u UserModel, t ThemeModel) {
	cu := &cachedUser{User: u, Theme: t, IconHash: fallbackIconHash}
	uc.mu.Lock()
	uc.byID[u.ID] = cu
	uc.byName[u.Name] = cu
	uc.mu.Unlock()
}

// アイコン更新（DB コミット後に呼ぶ）。エントリを差し替えるので読み手はロック無しで古い値を見続けてよい
func (uc *userCache) setIcon(userID int64, image []byte) {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	old, ok := uc.byID[userID]
	if !ok {
		return
	}
	cu := &cachedUser{User: old.User, Theme: old.Theme, Image: image, IconHash: fmt.Sprintf("%x", sha256.Sum256(image))}
	uc.byID[userID] = cu
	uc.byName[old.User.Name] = cu
}

func (cu *cachedUser) toUser() User {
	return User{
		ID:          cu.User.ID,
		Name:        cu.User.Name,
		DisplayName: cu.User.DisplayName,
		Description: cu.User.Description,
		Theme: Theme{
			ID:       cu.Theme.ID,
			DarkMode: cu.Theme.DarkMode,
		},
		IconHash: cu.IconHash,
	}
}

// id からユーザーのレスポンスを作る（キャッシュに無ければ DB）
func userResponseByID(ctx context.Context, tx *sqlx.Tx, id int64) (User, error) {
	if cu, ok := users.getByID(id); ok {
		return cu.toUser(), nil
	}
	um := UserModel{}
	if err := tx.GetContext(ctx, &um, "SELECT * FROM users WHERE id = ?", id); err != nil {
		return User{}, err
	}
	return fillUserResponse(ctx, tx, um)
}

// ---- タグ（不変）と配信ごとのタグ（配信作成時に確定、以後不変） ----

type tagCache struct {
	mu           sync.RWMutex
	all          []*Tag
	byID         map[int64]*Tag
	byName       map[string]*Tag
	byLivestream map[int64][]Tag
}

var tags = &tagCache{byID: map[int64]*Tag{}, byName: map[string]*Tag{}, byLivestream: map[int64][]Tag{}}

func (tc *tagCache) reload(ctx context.Context, db *sqlx.DB) error {
	var tagModels []TagModel
	if err := db.SelectContext(ctx, &tagModels, "SELECT * FROM tags ORDER BY id"); err != nil {
		return err
	}
	var lts []LivestreamTagModel
	if err := db.SelectContext(ctx, &lts, "SELECT * FROM livestream_tags ORDER BY id"); err != nil {
		return err
	}
	all := make([]*Tag, 0, len(tagModels))
	byID := make(map[int64]*Tag, len(tagModels))
	byName := make(map[string]*Tag, len(tagModels))
	for _, t := range tagModels {
		tag := &Tag{ID: t.ID, Name: t.Name}
		all = append(all, tag)
		byID[t.ID] = tag
		byName[t.Name] = tag
	}
	byLivestream := make(map[int64][]Tag)
	for _, lt := range lts {
		if tag, ok := byID[lt.TagID]; ok {
			byLivestream[lt.LivestreamID] = append(byLivestream[lt.LivestreamID], *tag)
		}
	}
	tc.mu.Lock()
	tc.all, tc.byID, tc.byName, tc.byLivestream = all, byID, byName, byLivestream
	tc.mu.Unlock()
	return nil
}

func (tc *tagCache) list() []*Tag {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.all
}

func (tc *tagCache) getByName(name string) (*Tag, bool) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	t, ok := tc.byName[name]
	return t, ok
}

func (tc *tagCache) getByID(id int64) (*Tag, bool) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	t, ok := tc.byID[id]
	return t, ok
}

// 配信のタグ。無ければ空スライス（nil ではなく [] を返す。JSON で [] にするため）
func (tc *tagCache) forLivestream(id int64) ([]Tag, bool) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	ts, ok := tc.byLivestream[id]
	if ts == nil {
		ts = []Tag{}
	}
	return ts, ok
}

// 配信作成（DB コミット後に呼ぶ）
func (tc *tagCache) setLivestreamTags(livestreamID int64, tagIDs []int64) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	ts := make([]Tag, 0, len(tagIDs))
	for _, id := range tagIDs {
		if t, ok := tc.byID[id]; ok {
			ts = append(ts, *t)
		}
	}
	tc.byLivestream[livestreamID] = ts
}

// ---- 配信（作成後は不変） ----

type livestreamCache struct {
	mu   sync.RWMutex
	byID map[int64]*LivestreamModel
}

var livestreams = &livestreamCache{byID: map[int64]*LivestreamModel{}}

func (lc *livestreamCache) reload(ctx context.Context, db *sqlx.DB) error {
	var models []LivestreamModel
	if err := db.SelectContext(ctx, &models, "SELECT * FROM livestreams"); err != nil {
		return err
	}
	byID := make(map[int64]*LivestreamModel, len(models))
	for i := range models {
		byID[models[i].ID] = &models[i]
	}
	lc.mu.Lock()
	lc.byID = byID
	lc.mu.Unlock()
	return nil
}

func (lc *livestreamCache) get(id int64) (*LivestreamModel, bool) {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	m, ok := lc.byID[id]
	return m, ok
}

// 配信作成（DB コミット後に呼ぶ）
func (lc *livestreamCache) add(m LivestreamModel) {
	lc.mu.Lock()
	lc.byID[m.ID] = &m
	lc.mu.Unlock()
}

// id から配信を引く（キャッシュに無ければ DB。無ければ sql.ErrNoRows）
func livestreamByID(ctx context.Context, tx *sqlx.Tx, id int64) (LivestreamModel, error) {
	if m, ok := livestreams.get(id); ok {
		return *m, nil
	}
	var m LivestreamModel
	if err := tx.GetContext(ctx, &m, "SELECT * FROM livestreams WHERE id = ?", id); err != nil {
		return LivestreamModel{}, err
	}
	return m, nil
}
