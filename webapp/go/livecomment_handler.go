package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v4"
)

type PostLivecommentRequest struct {
	Comment string `json:"comment"`
	Tip     int64  `json:"tip"`
}

type LivecommentModel struct {
	ID           int64  `db:"id"`
	UserID       int64  `db:"user_id"`
	LivestreamID int64  `db:"livestream_id"`
	Comment      string `db:"comment"`
	Tip          int64  `db:"tip"`
	CreatedAt    int64  `db:"created_at"`
}

type Livecomment struct {
	ID         int64      `json:"id"`
	User       User       `json:"user"`
	Livestream Livestream `json:"livestream"`
	Comment    string     `json:"comment"`
	Tip        int64      `json:"tip"`
	CreatedAt  int64      `json:"created_at"`
}

type LivecommentReport struct {
	ID          int64       `json:"id"`
	Reporter    User        `json:"reporter"`
	Livecomment Livecomment `json:"livecomment"`
	CreatedAt   int64       `json:"created_at"`
}

type LivecommentReportModel struct {
	ID            int64 `db:"id"`
	UserID        int64 `db:"user_id"`
	LivestreamID  int64 `db:"livestream_id"`
	LivecommentID int64 `db:"livecomment_id"`
	CreatedAt     int64 `db:"created_at"`
}

type ModerateRequest struct {
	NGWord string `json:"ng_word"`
}

type NGWord struct {
	ID           int64  `json:"id" db:"id"`
	UserID       int64  `json:"user_id" db:"user_id"`
	LivestreamID int64  `json:"livestream_id" db:"livestream_id"`
	Word         string `json:"word" db:"word"`
	CreatedAt    int64  `json:"created_at" db:"created_at"`
}

func getLivecommentsHandler(c echo.Context) error {
	ctx := c.Request().Context()

	if err := verifyUserSession(c); err != nil {
		// echo.NewHTTPErrorが返っているのでそのまま出力
		return err
	}

	livestreamID, err := strconv.Atoi(c.Param("livestream_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livestream_id in path must be integer")
	}

	limit := -1
	if c.QueryParam("limit") != "" {
		l, err := strconv.Atoi(c.QueryParam("limit"))
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "limit query parameter must be integer")
		}
		limit = l
	}
	tx := dbConn
	livecommentModels := livecomments.list(int64(livestreamID), limit)

	res := make([]Livecomment, len(livecommentModels))
	for i := range livecommentModels {
		livecomment, err := fillLivecommentResponse(ctx, tx, livecommentModels[i])
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to fil livecomments: "+err.Error())
		}

		res[i] = livecomment
	}

	return c.JSON(http.StatusOK, res)
}

func getNgwords(c echo.Context) error {
	if err := verifyUserSession(c); err != nil {
		return err
	}

	userID := sessionUserID(c)

	livestreamID, err := strconv.Atoi(c.Param("livestream_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livestream_id in path must be integer")
	}

	// キャッシュから（元実装と同じく created_at 降順。無ければ null）
	all := ngWords.forLivestream(int64(livestreamID))
	var ngWordList []*NGWord
	for i := range all {
		if all[i].UserID == userID {
			w := all[i]
			ngWordList = append(ngWordList, &w)
		}
	}
	sort.SliceStable(ngWordList, func(i, j int) bool {
		if ngWordList[i].CreatedAt == ngWordList[j].CreatedAt {
			return ngWordList[i].ID > ngWordList[j].ID
		}
		return ngWordList[i].CreatedAt > ngWordList[j].CreatedAt
	})

	return c.JSON(http.StatusOK, ngWordList)
}

func postLivecommentHandler(c echo.Context) error {
	ctx := c.Request().Context()
	defer c.Request().Body.Close()

	if err := verifyUserSession(c); err != nil {
		return err
	}

	livestreamID, err := strconv.Atoi(c.Param("livestream_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livestream_id in path must be integer")
	}

	userID := sessionUserID(c)

	var req *PostLivecommentRequest
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "failed to decode the request body as json")
	}

	// 書き込みは1文だけなのでトランザクションを張らない（BEGIN/COMMIT の往復を減らす）
	tx := dbConn

	livestreamModel, err := livestreamByID(ctx, tx, int64(livestreamID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "livestream not found")
		} else {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livestream: "+err.Error())
		}
	}

	// スパム判定（配信者が登録した NG ワードを含むか。LIKE '%word%' と同じ判定を Go で行う）
	for _, ngword := range ngWords.forLivestream(livestreamModel.ID) {
		if ngword.UserID != livestreamModel.UserID {
			continue
		}
		hit, ok := likeContains(req.Comment, ngword.Word)
		if !ok {
			// ワイルドカードを含む語だけ SQL に任せる
			var hitSpam int
			if err := sqlx.GetContext(ctx, tx, &hitSpam, "SELECT (? LIKE CONCAT('%', ?, '%'))", req.Comment, ngword.Word); err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "failed to get hitspam: "+err.Error())
			}
			hit = hitSpam >= 1
		}
		if hit {
			return echo.NewHTTPError(http.StatusBadRequest, "このコメントがスパム判定されました")
		}
	}

	now := time.Now().Unix()
	livecommentModel := LivecommentModel{
		UserID:       userID,
		LivestreamID: int64(livestreamID),
		Comment:      req.Comment,
		Tip:          req.Tip,
		CreatedAt:    now,
	}

	rs, err := tx.NamedExecContext(ctx, "INSERT INTO livecomments (user_id, livestream_id, comment, tip, created_at) VALUES (:user_id, :livestream_id, :comment, :tip, :created_at)", livecommentModel)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to insert livecomment: "+err.Error())
	}

	livecommentID, err := rs.LastInsertId()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get last inserted livecomment id: "+err.Error())
	}
	livecommentModel.ID = livecommentID

	livecomment, err := fillLivecommentResponse(ctx, tx, livecommentModel)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fill livecomment: "+err.Error())
	}

	scores.add(int64(livestreamID), req.Tip)
	livecomments.add(livecommentModel)

	return c.JSON(http.StatusCreated, livecomment)
}

func reportLivecommentHandler(c echo.Context) error {
	ctx := c.Request().Context()

	if err := verifyUserSession(c); err != nil {
		return err
	}

	livestreamID, err := strconv.Atoi(c.Param("livestream_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livestream_id in path must be integer")
	}

	livecommentID, err := strconv.Atoi(c.Param("livecomment_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livecomment_id in path must be integer")
	}

	userID := sessionUserID(c)

	// 書き込みは1文だけなのでトランザクションを張らない（BEGIN/COMMIT の往復を減らす）
	tx := dbConn

	if _, err := livestreamByID(ctx, tx, int64(livestreamID)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "livestream not found")
		} else {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livestream: "+err.Error())
		}
	}

	var livecommentModel LivecommentModel
	if err := sqlx.GetContext(ctx, tx, &livecommentModel, "SELECT * FROM livecomments WHERE id = ?", livecommentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "livecomment not found")
		} else {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livecomment: "+err.Error())
		}
	}

	now := time.Now().Unix()
	reportModel := LivecommentReportModel{
		UserID:        int64(userID),
		LivestreamID:  int64(livestreamID),
		LivecommentID: int64(livecommentID),
		CreatedAt:     now,
	}
	rs, err := tx.NamedExecContext(ctx, "INSERT INTO livecomment_reports(user_id, livestream_id, livecomment_id, created_at) VALUES (:user_id, :livestream_id, :livecomment_id, :created_at)", &reportModel)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to insert livecomment report: "+err.Error())
	}
	reportID, err := rs.LastInsertId()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get last inserted livecomment report id: "+err.Error())
	}
	reportModel.ID = reportID

	report, err := fillLivecommentReportResponse(ctx, tx, reportModel)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fill livecomment report: "+err.Error())
	}

	return c.JSON(http.StatusCreated, report)
}

// NGワードを登録
func moderateHandler(c echo.Context) error {
	ctx := c.Request().Context()
	defer c.Request().Body.Close()

	if err := verifyUserSession(c); err != nil {
		return err
	}

	livestreamID, err := strconv.Atoi(c.Param("livestream_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livestream_id in path must be integer")
	}

	userID := sessionUserID(c)

	var req *ModerateRequest
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "failed to decode the request body as json")
	}

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	// 配信者自身の配信に対するmoderateなのかを検証
	var ownedLivestreams []LivestreamModel
	if err := sqlx.SelectContext(ctx, tx, &ownedLivestreams, "SELECT * FROM livestreams WHERE id = ? AND user_id = ?", livestreamID, userID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livestreams: "+err.Error())
	}
	if len(ownedLivestreams) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "A streamer can't moderate livestreams that other streamers own")
	}

	ngWordCreatedAt := time.Now().Unix()
	rs, err := tx.NamedExecContext(ctx, "INSERT INTO ng_words(user_id, livestream_id, word, created_at) VALUES (:user_id, :livestream_id, :word, :created_at)", &NGWord{
		UserID:       int64(userID),
		LivestreamID: int64(livestreamID),
		Word:         req.NGWord,
		CreatedAt:    ngWordCreatedAt,
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to insert new NG word: "+err.Error())
	}

	wordID, err := rs.LastInsertId()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get last inserted NG word id: "+err.Error())
	}

	var ngwords []*NGWord
	if err := sqlx.SelectContext(ctx, tx, &ngwords, "SELECT * FROM ng_words WHERE livestream_id = ?", livestreamID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get NG words: "+err.Error())
	}

	// NGワードにヒットする過去の投稿も全削除する
	// （元実装は全ライブコメントを取ってきて 1件ずつ DELETE していた。LIKE の意味は同じ）
	var deletedTips int64
	for _, ngword := range ngwords {
		var t int64
		if err := sqlx.GetContext(ctx, tx, &t, "SELECT IFNULL(SUM(tip), 0) FROM livecomments WHERE livestream_id = ? AND comment LIKE CONCAT('%', ?, '%')", livestreamID, ngword.Word); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to sum old livecomments that hit spams: "+err.Error())
		}
		deletedTips += t
		if _, err := tx.ExecContext(ctx, "DELETE FROM livecomments WHERE livestream_id = ? AND comment LIKE CONCAT('%', ?, '%')", livestreamID, ngword.Word); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to delete old livecomments that hit spams: "+err.Error())
		}
	}

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}
	scores.add(int64(livestreamID), -deletedTips)
	{
		words := make([]string, 0, len(ngwords))
		wildcard := false
		for _, w := range ngwords {
			words = append(words, w.Word)
			if strings.ContainsAny(w.Word, `%_\`) {
				wildcard = true
			}
		}
		if wildcard {
			if err := livecomments.reloadLivestream(ctx, dbConn, int64(livestreamID)); err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "failed to reload livecomments: "+err.Error())
			}
		} else {
			livecomments.removeMatching(int64(livestreamID), words)
		}
	}
	ngWords.add(NGWord{ID: wordID, UserID: int64(userID), LivestreamID: int64(livestreamID), Word: req.NGWord, CreatedAt: ngWordCreatedAt})

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"word_id": wordID,
	})
}

func fillLivecommentResponse(ctx context.Context, tx sqlx.QueryerContext, livecommentModel LivecommentModel) (Livecomment, error) {
	commentOwner, err := userResponseByID(ctx, tx, livecommentModel.UserID)
	if err != nil {
		return Livecomment{}, err
	}

	livestreamModel, err := livestreamByID(ctx, tx, livecommentModel.LivestreamID)
	if err != nil {
		return Livecomment{}, err
	}
	livestream, err := fillLivestreamResponse(ctx, tx, livestreamModel)
	if err != nil {
		return Livecomment{}, err
	}

	livecomment := Livecomment{
		ID:         livecommentModel.ID,
		User:       commentOwner,
		Livestream: livestream,
		Comment:    livecommentModel.Comment,
		Tip:        livecommentModel.Tip,
		CreatedAt:  livecommentModel.CreatedAt,
	}

	return livecomment, nil
}

func fillLivecommentReportResponse(ctx context.Context, tx sqlx.QueryerContext, reportModel LivecommentReportModel) (LivecommentReport, error) {
	reporter, err := userResponseByID(ctx, tx, reportModel.UserID)
	if err != nil {
		return LivecommentReport{}, err
	}

	livecommentModel := LivecommentModel{}
	if err := sqlx.GetContext(ctx, tx, &livecommentModel, "SELECT * FROM livecomments WHERE id = ?", reportModel.LivecommentID); err != nil {
		return LivecommentReport{}, err
	}
	livecomment, err := fillLivecommentResponse(ctx, tx, livecommentModel)
	if err != nil {
		return LivecommentReport{}, err
	}

	report := LivecommentReport{
		ID:          reportModel.ID,
		Reporter:    reporter,
		Livecomment: livecomment,
		CreatedAt:   reportModel.CreatedAt,
	}
	return report, nil
}
