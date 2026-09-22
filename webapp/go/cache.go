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
	"sort"
	"strings"
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
	mu        sync.RWMutex
	byID      map[int64]*cachedUser
	byName    map[string]*cachedUser
	lowerName map[string]struct{} // DNS 用（名前は大文字を含みうるが DNS は大文字小文字を区別しない）
}

var (
	users             = &userCache{byID: map[int64]*cachedUser{}, byName: map[string]*cachedUser{}, lowerName: map[string]struct{}{}}
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
	lowerName := make(map[string]struct{}, len(userModels))
	for _, u := range userModels {
		cu := &cachedUser{User: u, IconHash: fallbackIconHash}
		byID[u.ID] = cu
		byName[u.Name] = cu
		lowerName[strings.ToLower(u.Name)] = struct{}{}
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
	uc.lowerName = lowerName
	uc.mu.Unlock()
	return nil
}

// DNS 用: 小文字化した名前が登録されているか
func (uc *userCache) hasLowerName(lower string) bool {
	uc.mu.RLock()
	defer uc.mu.RUnlock()
	_, ok := uc.lowerName[lower]
	return ok
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
	uc.lowerName[strings.ToLower(u.Name)] = struct{}{}
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
func userResponseByID(ctx context.Context, tx sqlx.QueryerContext, id int64) (User, error) {
	if cu, ok := users.getByID(id); ok {
		return cu.toUser(), nil
	}
	um := UserModel{}
	if err := sqlx.GetContext(ctx, tx, &um, "SELECT * FROM users WHERE id = ?", id); err != nil {
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
func livestreamByID(ctx context.Context, tx sqlx.QueryerContext, id int64) (LivestreamModel, error) {
	if m, ok := livestreams.get(id); ok {
		return *m, nil
	}
	var m LivestreamModel
	if err := sqlx.GetContext(ctx, tx, &m, "SELECT * FROM livestreams WHERE id = ?", id); err != nil {
		return LivestreamModel{}, err
	}
	return m, nil
}

// ---- ランキング用スコア（配信ごと: リアクション数 + 投げ銭合計。ユーザーは自分の配信の合計） ----
//
// 統計 API のランキングは全配信/全ユーザーのスコアを集計してソートしていた（1回 150〜230ms、DB 時間の 33%）。
// スコアの増減はリアクション投稿・ライブコメント投稿・モデレーションによる削除だけなので、メモリで数える。
// DB が正。起動時と initialize で DB から作り直す。更新は DB コミット後。

type scoreCache struct {
	mu   sync.RWMutex
	ls   map[int64]int64 // livestream_id -> score
	user map[int64]int64 // user_id -> score
}

var scores = &scoreCache{ls: map[int64]int64{}, user: map[int64]int64{}}

func (sc *scoreCache) reload(ctx context.Context, db *sqlx.DB) error {
	type row struct {
		LivestreamID int64 `db:"livestream_id"`
		UserID       int64 `db:"user_id"`
		Score        int64 `db:"score"`
	}
	var rows []row
	if err := db.SelectContext(ctx, &rows, `
		SELECT l.id AS livestream_id, l.user_id AS user_id, IFNULL(r.cnt, 0) + IFNULL(t.tips, 0) AS score
		FROM livestreams l
		LEFT JOIN (SELECT livestream_id, COUNT(*) AS cnt FROM reactions GROUP BY livestream_id) r ON r.livestream_id = l.id
		LEFT JOIN (SELECT livestream_id, IFNULL(SUM(tip), 0) AS tips FROM livecomments GROUP BY livestream_id) t ON t.livestream_id = l.id`); err != nil {
		return err
	}
	ls := make(map[int64]int64, len(rows))
	user := make(map[int64]int64)
	for _, r := range rows {
		ls[r.LivestreamID] = r.Score
		user[r.UserID] += r.Score
	}
	sc.mu.Lock()
	sc.ls, sc.user = ls, user
	sc.mu.Unlock()
	return nil
}

// 配信のスコアに delta を足す（DB コミット後に呼ぶ）。オーナーのスコアにも反映
func (sc *scoreCache) add(livestreamID int64, delta int64) {
	if delta == 0 {
		return
	}
	owner := int64(0)
	if m, ok := livestreams.get(livestreamID); ok {
		owner = m.UserID
	}
	sc.mu.Lock()
	sc.ls[livestreamID] += delta
	if owner != 0 {
		sc.user[owner] += delta
	}
	sc.mu.Unlock()
}

// 配信のランク（スコア降順。同点は id の小さい方が上位 = 元実装と同じ）
func (sc *scoreCache) livestreamRank(id int64) int64 {
	livestreams.mu.RLock()
	ids := make([]int64, 0, len(livestreams.byID))
	for k := range livestreams.byID {
		ids = append(ids, k)
	}
	livestreams.mu.RUnlock()

	sc.mu.RLock()
	ranking := make(LivestreamRanking, 0, len(ids))
	for _, k := range ids {
		ranking = append(ranking, LivestreamRankingEntry{LivestreamID: k, Score: sc.ls[k]})
	}
	sc.mu.RUnlock()
	sort.Sort(ranking)
	var rank int64 = 1
	for i := len(ranking) - 1; i >= 0; i-- {
		if ranking[i].LivestreamID == id {
			break
		}
		rank++
	}
	return rank
}

// ユーザーのランク（スコア降順。同点は名前の小さい方が上位 = 元実装と同じ）
func (sc *scoreCache) userRank(name string) int64 {
	users.mu.RLock()
	entries := make([]UserRankingEntry, 0, len(users.byID))
	sc.mu.RLock()
	for id, cu := range users.byID {
		entries = append(entries, UserRankingEntry{Username: cu.User.Name, Score: sc.user[id]})
	}
	sc.mu.RUnlock()
	users.mu.RUnlock()
	ranking := UserRanking(entries)
	sort.Sort(ranking)
	var rank int64 = 1
	for i := len(ranking) - 1; i >= 0; i-- {
		if ranking[i].Username == name {
			break
		}
		rank++
	}
	return rank
}
