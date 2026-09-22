package main

import (
	"net/http"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v4"
)

type PaymentResult struct {
	TotalTip int64 `json:"total_tip"`
}

func GetPaymentResult(c echo.Context) error {
	ctx := c.Request().Context()

	// 読み取りだけなのでトランザクションを張らない（BEGIN/COMMIT の往復を減らす）
	tx := dbConn

	var totalTip int64
	if err := sqlx.GetContext(ctx, tx, &totalTip, "SELECT IFNULL(SUM(tip), 0) FROM livecomments"); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to count total tip: "+err.Error())
	}

	return c.JSON(http.StatusOK, &PaymentResult{
		TotalTip: totalTip,
	})
}
