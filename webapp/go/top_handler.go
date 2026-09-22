package main

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v4"
)

type Tag struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type TagModel struct {
	ID   int64  `db:"id"`
	Name string `db:"name"`
}

type TagsResponse struct {
	Tags []*Tag `json:"tags"`
}

func getTagHandler(c echo.Context) error {
	return c.JSON(http.StatusOK, &TagsResponse{
		Tags: tags.list(),
	})
}

// 配信者のテーマ取得API
// GET /api/user/:username/theme
func getStreamerThemeHandler(c echo.Context) error {
	ctx := c.Request().Context()

	if err := verifyUserSession(c); err != nil {
		// echo.NewHTTPErrorが返っているのでそのまま出力
		c.Logger().Printf("verifyUserSession: %+v\n", err)
		return err
	}

	username := c.Param("username")

	// 読み取りだけなのでトランザクションを張らない（BEGIN/COMMIT の往復を減らす）
	tx := dbConn

	userModel := UserModel{}
	err := sqlx.GetContext(ctx, tx, &userModel, "SELECT id FROM users WHERE name = ?", username)
	if errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusNotFound, "not found user that has the given username")
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get user: "+err.Error())
	}

	themeModel := ThemeModel{}
	if err := sqlx.GetContext(ctx, tx, &themeModel, "SELECT * FROM themes WHERE user_id = ?", userModel.ID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get user theme: "+err.Error())
	}

	theme := Theme{
		ID:       themeModel.ID,
		DarkMode: themeModel.DarkMode,
	}

	return c.JSON(http.StatusOK, theme)
}
