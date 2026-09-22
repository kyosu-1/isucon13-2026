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
	icons, err := loadIconFiles()
	if err != nil {
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
	for uid, image := range icons {
		if cu, ok := byID[uid]; ok {
			cu.Image = image
			cu.IconHash = fmt.Sprintf("%x", sha256.Sum256(image))
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

// ---- NG ワード（配信ごと。モデレーションで増えるだけ） ----

type ngWordCache struct {
	mu           sync.RWMutex
	byLivestream map[int64][]NGWord
}

var ngWords = &ngWordCache{byLivestream: map[int64][]NGWord{}}

func (nc *ngWordCache) reload(ctx context.Context, db *sqlx.DB) error {
	var rows []NGWord
	if err := db.SelectContext(ctx, &rows, "SELECT * FROM ng_words ORDER BY id"); err != nil {
		return err
	}
	m := make(map[int64][]NGWord)
	for _, w := range rows {
		m[w.LivestreamID] = append(m[w.LivestreamID], w)
	}
	nc.mu.Lock()
	nc.byLivestream = m
	nc.mu.Unlock()
	return nil
}

// 配信の NG ワード（登録順）。呼び出し側で変更しないこと
func (nc *ngWordCache) forLivestream(livestreamID int64) []NGWord {
	nc.mu.RLock()
	defer nc.mu.RUnlock()
	return nc.byLivestream[livestreamID]
}

// 追加（DB コミット後に呼ぶ）。スライスは差し替える（読み手はコピーを持ち続けてよい）
func (nc *ngWordCache) add(w NGWord) {
	nc.mu.Lock()
	old := nc.byLivestream[w.LivestreamID]
	ws := make([]NGWord, 0, len(old)+1)
	ws = append(ws, old...)
	ws = append(ws, w)
	nc.byLivestream[w.LivestreamID] = ws
	nc.mu.Unlock()
}

// MySQL の LIKE '%word%'（utf8mb4_bin なのでバイト単位の完全一致）と同じ判定。
// ワイルドカード（% _ \）を含む語は LIKE の意味が変わるので ok=false を返し、呼び出し側で SQL に任せる
func likeContains(text, word string) (hit bool, ok bool) {
	if strings.ContainsAny(word, `%_\`) {
		return false, false
	}
	return strings.Contains(text, word), true
}

// ---- アイコン画像はローカルファイルに置く ----
//
// icons テーブルへの LONGBLOB の upsert が 1回 5ms・DB 時間の 31% だった。
// 画像は <iconDir>/<user_id> に書く（tmp に書いて rename）。再起動後もファイルは残るので、
// 起動時にここから読み直す。initialize で全部消す。DB の icons テーブルは使わない。

const iconDir = "../icons"

func loadIconFiles() (map[int64][]byte, error) {
	if err := os.MkdirAll(iconDir, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(iconDir)
	if err != nil {
		return nil, err
	}
	out := make(map[int64][]byte, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		var uid int64
		if _, err := fmt.Sscanf(e.Name(), "%d", &uid); err != nil {
			continue // tmp ファイルなど
		}
		b, err := os.ReadFile(iconDir + "/" + e.Name())
		if err != nil {
			return nil, err
		}
		out[uid] = b
	}
	return out, nil
}

func saveIconFile(userID int64, image []byte) error {
	tmp := fmt.Sprintf("%s/.tmp-%d-%d", iconDir, userID, os.Getpid())
	if err := os.WriteFile(tmp, image, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, fmt.Sprintf("%s/%d", iconDir, userID))
}

func clearIconFiles() error {
	if err := os.RemoveAll(iconDir); err != nil {
		return err
	}
	return os.MkdirAll(iconDir, 0o755)
}

// ---- セッション Cookie のデコード結果のメモ ----
//
// gorilla/sessions の CookieStore は毎リクエスト HMAC 検証 + base64 + gob デコード（pprof で gob が 8%）。
// Cookie の値はログインまで変わらないので、値 → 中身 をメモしておく。値そのものが鍵なので改竄は効かない。

type sessionInfo struct {
	UserID   int64
	Username string
	Expires  int64
}

var sessionMemo sync.Map // string(cookie value) -> *sessionInfo

// ---- 配信ごとのライブコメント・リアクション（一覧 API 用） ----
//
// GET /api/livestream/:id/livecomment と /reaction が DB 時間の 32%（各 27k 回/分）。
// どちらも「その配信のものを created_at 降順（同時刻は id 降順）で limit 件」なので、配信ごとに id 昇順で持ち、
// 後ろから返す。追加は投稿のコミット後、削除はモデレーションのコミット後。起動時と initialize で DB から作り直す。

type livecommentCache struct {
	mu           sync.RWMutex
	byLivestream map[int64][]LivecommentModel
}

type reactionCache struct {
	mu           sync.RWMutex
	byLivestream map[int64][]ReactionModel
}

var (
	livecomments = &livecommentCache{byLivestream: map[int64][]LivecommentModel{}}
	reactions    = &reactionCache{byLivestream: map[int64][]ReactionModel{}}
)

func (lc *livecommentCache) reload(ctx context.Context, db *sqlx.DB) error {
	var rows []LivecommentModel
	if err := db.SelectContext(ctx, &rows, "SELECT * FROM livecomments ORDER BY created_at, id"); err != nil {
		return err
	}
	m := make(map[int64][]LivecommentModel)
	for _, r := range rows {
		m[r.LivestreamID] = append(m[r.LivestreamID], r)
	}
	lc.mu.Lock()
	lc.byLivestream = m
	lc.mu.Unlock()
	return nil
}

// created_at 降順（同時刻は id 降順）で最大 limit 件（limit<0 なら全件）
func (lc *livecommentCache) list(livestreamID int64, limit int) []LivecommentModel {
	lc.mu.RLock()
	defer lc.mu.RUnlock()
	all := lc.byLivestream[livestreamID]
	n := len(all)
	if limit >= 0 && limit < n {
		n = limit
	}
	out := make([]LivecommentModel, 0, n)
	for i := len(all) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, all[i])
	}
	return out
}

// 投稿（DB コミット後）。created_at は単調増加なので末尾に足す
func (lc *livecommentCache) add(m LivecommentModel) {
	lc.mu.Lock()
	lc.byLivestream[m.LivestreamID] = append(lc.byLivestream[m.LivestreamID], m)
	lc.mu.Unlock()
}

// モデレーション（DB コミット後）: 配信内で語を含むコメントを消す。消した tip の合計を返す
func (lc *livecommentCache) removeMatching(livestreamID int64, words []string) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	all := lc.byLivestream[livestreamID]
	kept := all[:0:0]
	for _, c := range all {
		hit := false
		for _, w := range words {
			if h, ok := likeContains(c.Comment, w); ok && h {
				hit = true
				break
			}
		}
		if !hit {
			kept = append(kept, c)
		}
	}
	lc.byLivestream[livestreamID] = kept
}

// ワイルドカードを含む語があるときは DB から読み直す
func (lc *livecommentCache) reloadLivestream(ctx context.Context, db sqlx.QueryerContext, livestreamID int64) error {
	var rows []LivecommentModel
	if err := sqlx.SelectContext(ctx, db, &rows, "SELECT * FROM livecomments WHERE livestream_id = ? ORDER BY created_at, id", livestreamID); err != nil {
		return err
	}
	lc.mu.Lock()
	lc.byLivestream[livestreamID] = rows
	lc.mu.Unlock()
	return nil
}

func (rc *reactionCache) reload(ctx context.Context, db *sqlx.DB) error {
	var rows []ReactionModel
	if err := db.SelectContext(ctx, &rows, "SELECT * FROM reactions ORDER BY created_at, id"); err != nil {
		return err
	}
	m := make(map[int64][]ReactionModel)
	for _, r := range rows {
		m[r.LivestreamID] = append(m[r.LivestreamID], r)
	}
	rc.mu.Lock()
	rc.byLivestream = m
	rc.mu.Unlock()
	return nil
}

func (rc *reactionCache) list(livestreamID int64, limit int) []ReactionModel {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	all := rc.byLivestream[livestreamID]
	n := len(all)
	if limit >= 0 && limit < n {
		n = limit
	}
	out := make([]ReactionModel, 0, n)
	for i := len(all) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, all[i])
	}
	return out
}

func (rc *reactionCache) add(m ReactionModel) {
	rc.mu.Lock()
	rc.byLivestream[m.LivestreamID] = append(rc.byLivestream[m.LivestreamID], m)
	rc.mu.Unlock()
}
