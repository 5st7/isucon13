package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo-contrib/session"
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

	v, ok := LivecommentCache.Get(fmt.Sprintf("%d", livestreamID))
	if ok {
		livecomments, ok := v.([]Livestream)
		if ok {
			return c.JSON(http.StatusOK, livecomments)
		}
	}

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	query := "SELECT tags.id as id, tags.name as name from livestream_tags INNER JOIN tags ON tags.id = livestream_tags.tag_id WHERE livestream_tags.livestream_id = ?"
	tags := make([]Tag, 0)
	err = tx.SelectContext(ctx, &tags, query, livestreamID)
	if errors.Is(err, sql.ErrNoRows) {
		return c.JSON(http.StatusOK, []*Livecomment{})
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livecomments: "+err.Error())
	}

	// live stream owner
	type LivestreamA struct {
		// live stream owner
		OwnerID            int64          `db:"user_id"`
		OwnerName          string         `db:"user_name"`
		OwnerDisplayName   string         `db:"user_display_name"`
		OwnerDescription   string         `db:"user_description"`
		OwnerImageHash     sql.NullString `db:"user_image_hash"`
		OwnerThemeId       int64          `db:"user_theme_id"`
		OwnerThemeDarkMode bool           `db:"user_theme_dark_mode"`

		// live stream
		LiveStreamID           int64  `db:"live_stream_id"`
		LiveStreamTitle        string `db:"live_stream_title"`
		LiveStreamDescription  string `db:"live_stream_description"`
		LiveStreamPlaylistUrl  string `db:"live_stream_playlist_url"`
		LiveStreamThumbnailUrl string `db:"live_stream_thumbnail_url"`
		LiveStreamStartAt      int64  `db:"live_stream_start_at"`
		LiveStreamEndAt        int64  `db:"live_stream_end_at"`
	}

	query = "SELECT " +
		"users.id as user_id," +
		"users.name as user_name," +
		"users.display_name as user_display_name," +
		"users.description as user_description," +
		"icons.hash as user_image_hash," +
		"themes.id as user_theme_id," +
		"themes.dark_mode as user_theme_dark_mode," +
		"livestreams.id as live_stream_id," +
		"livestreams.title as live_stream_title," +
		"livestreams.description as live_stream_description," +
		"livestreams.playlist_url as live_stream_playlist_url," +
		"livestreams.thumbnail_url as live_stream_thumbnail_url," +
		"livestreams.start_at as live_stream_start_at," +
		"livestreams.end_at as live_stream_end_at " +
		"FROM livestreams " +
		"INNER JOIN users ON users.id = livestreams.user_id " +
		"LEFT JOIN icons ON icons.user_id = users.id " +
		"INNER JOIN themes ON themes.user_id = users.id " +
		"WHERE livestreams.id = ? "

	fmt.Println("query: ", query)

	var livestreamModel []LivestreamA
	err = tx.SelectContext(ctx, &livestreamModel, query, livestreamID)
	if errors.Is(err, sql.ErrNoRows) {
		return c.JSON(http.StatusOK, []*Livecomment{})
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livestreams: "+err.Error())
	}
	if len(livestreamModel) == 0 {
		return c.JSON(http.StatusOK, []*Livecomment{})
	}

	hash := livestreamModel[0].OwnerImageHash.String
	if !livestreamModel[0].OwnerImageHash.Valid {
		file, err := os.ReadFile(fallbackImage)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to read fallback image: "+err.Error())
		}
		hash = fmt.Sprintf("%x", sha256.Sum256(file))
	}

	livestream := Livestream{
		ID: livestreamModel[0].LiveStreamID,
		Owner: User{
			ID:          livestreamModel[0].OwnerID,
			Name:        livestreamModel[0].OwnerName,
			DisplayName: livestreamModel[0].OwnerDisplayName,
			Description: livestreamModel[0].OwnerDescription,
			Theme: Theme{
				ID:       livestreamModel[0].OwnerThemeId,
				DarkMode: livestreamModel[0].OwnerThemeDarkMode,
			},
			IconHash: hash,
		},
		Title:        livestreamModel[0].LiveStreamTitle,
		Description:  livestreamModel[0].LiveStreamDescription,
		PlaylistUrl:  livestreamModel[0].LiveStreamPlaylistUrl,
		ThumbnailUrl: livestreamModel[0].LiveStreamThumbnailUrl,
		Tags:         tags,
		StartAt:      livestreamModel[0].LiveStreamStartAt,
		EndAt:        livestreamModel[0].LiveStreamEndAt,
	}

	type Response struct {
		// live comment
		ID        int64  `db:"live_comment_id"`
		Comment   string `db:"live_comment_comment"`
		Tip       int64  `db:"live_comment_tip"`
		CreatedAt int64  `db:"live_comment_created_at"`

		// User
		UserID          int64  `db:"user_id"`
		UserName        string `db:"user_name"`
		UserDisplayName string `db:"user_display_name"`
		UserDescription string `db:"user_description"`

		// icon
		UserImageHash sql.NullString `db:"user_image_hash"`

		// theme
		ThemeID       int64 `db:"theme_id"`
		ThemeDarkMode bool  `db:"theme_dark_mode"`
	}

	query = "SELECT " +
		"users.id as user_id," +
		"users.name as user_name," +
		"users.display_name as user_display_name," +
		"users.description as user_description," +
		"icons.hash as user_image_hash," +
		"themes.id as theme_id," +
		"themes.dark_mode as theme_dark_mode," +
		"livecomments.id as live_comment_id," +
		"livecomments.comment as live_comment_comment," +
		"livecomments.tip as live_comment_tip," +
		"livecomments.created_at as live_comment_created_at" +
		" FROM livecomments" +
		" INNER JOIN users ON users.id = livecomments.user_id" +
		" INNER JOIN livestreams ON livestreams.id = livecomments.livestream_id" +
		" INNER JOIN themes ON themes.user_id = users.id" +
		" LEFT JOIN icons ON icons.user_id = users.id" +
		" WHERE livecomments.livestream_id = ?" +
		" ORDER BY created_at DESC"

	if c.QueryParam("limit") != "" {
		limit, err := strconv.Atoi(c.QueryParam("limit"))
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "limit query parameter must be integer")
		}
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	fmt.Println("query: ", query)

	response := []Response{}
	err = tx.SelectContext(ctx, &response, query, livestreamID)
	if errors.Is(err, sql.ErrNoRows) {
		return c.JSON(http.StatusOK, []*Livecomment{})
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livecomments: "+err.Error())
	}

	livecomments := make([]Livecomment, len(response))
	for i := range response {
		hash := response[i].UserImageHash.String
		if !response[i].UserImageHash.Valid {
			file, err := os.ReadFile(fallbackImage)
			if err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, "failed to read fallback image: "+err.Error())
			}
			hash = fmt.Sprintf("%x", sha256.Sum256(file))
		}

		comment := Livecomment{
			ID: response[i].ID,
			User: User{
				ID:          response[i].UserID,
				Name:        response[i].UserName,
				DisplayName: response[i].UserDisplayName,
				Description: response[i].UserDisplayName,
				Theme: Theme{
					ID:       response[i].ThemeID,
					DarkMode: response[i].ThemeDarkMode,
				},
				IconHash: hash,
			},
			Livestream: livestream,
			Comment:    response[i].Comment,
			Tip:        response[i].Tip,
			CreatedAt:  response[i].CreatedAt,
		}
		livecomments[i] = comment
	}

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}

	LivecommentCache.Add(fmt.Sprintf("%d", livestreamID), livecomments)

	return c.JSON(http.StatusOK, livecomments)
}

func getNgwords(c echo.Context) error {
	ctx := c.Request().Context()

	if err := verifyUserSession(c); err != nil {
		return err
	}

	// error already checked
	sess, _ := session.Get(defaultSessionIDKey, c)
	// existence already checked
	userID := sess.Values[defaultUserIDKey].(int64)

	livestreamID, err := strconv.Atoi(c.Param("livestream_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "livestream_id in path must be integer")
	}

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	var ngWords []*NGWord
	if err := tx.SelectContext(ctx, &ngWords, "SELECT * FROM ng_words WHERE user_id = ? AND livestream_id = ? ORDER BY created_at DESC", userID, livestreamID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.JSON(http.StatusOK, []*NGWord{})
		} else {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get NG words: "+err.Error())
		}
	}

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}

	return c.JSON(http.StatusOK, ngWords)
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
	LivecommentCache.Remove(fmt.Sprintf("%d", livestreamID))

	// error already checked
	sess, _ := session.Get(defaultSessionIDKey, c)
	// existence already checked
	userID := sess.Values[defaultUserIDKey].(int64)

	var req *PostLivecommentRequest
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "failed to decode the request body as json")
	}

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	var livestreamModel LivestreamModel
	if err := tx.GetContext(ctx, &livestreamModel, "SELECT * FROM livestreams WHERE id = ?", livestreamID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "livestream not found")
		} else {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livestream: "+err.Error())
		}
	}

	// スパム判定
	var ngwords []*NGWord
	if err := tx.SelectContext(ctx, &ngwords, "SELECT id, user_id, livestream_id, word FROM ng_words WHERE user_id = ? AND livestream_id = ?", livestreamModel.UserID, livestreamModel.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get NG words: "+err.Error())
	}

	var hitSpam int
	for _, ngword := range ngwords {
		query := `
		SELECT COUNT(*)
		FROM
		(SELECT ? AS text) AS texts
		INNER JOIN
		(SELECT CONCAT('%', ?, '%')	AS pattern) AS patterns
		ON texts.text LIKE patterns.pattern;
		`
		if err := tx.GetContext(ctx, &hitSpam, query, req.Comment, ngword.Word); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get hitspam: "+err.Error())
		}
		c.Logger().Infof("[hitSpam=%d] comment = %s", hitSpam, req.Comment)
		if hitSpam >= 1 {
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

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}

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

	// error already checked
	sess, _ := session.Get(defaultSessionIDKey, c)
	// existence already checked
	userID := sess.Values[defaultUserIDKey].(int64)

	tx, err := dbConn.BeginTxx(ctx, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to begin transaction: "+err.Error())
	}
	defer tx.Rollback()

	var livestreamModel LivestreamModel
	if err := tx.GetContext(ctx, &livestreamModel, "SELECT * FROM livestreams WHERE id = ?", livestreamID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "livestream not found")
		} else {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livestream: "+err.Error())
		}
	}

	var livecommentModel LivecommentModel
	if err := tx.GetContext(ctx, &livecommentModel, "SELECT * FROM livecomments WHERE id = ?", livecommentID); err != nil {
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
	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
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

	// error already checked
	sess, _ := session.Get(defaultSessionIDKey, c)
	// existence already checked
	userID := sess.Values[defaultUserIDKey].(int64)

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
	if err := tx.SelectContext(ctx, &ownedLivestreams, "SELECT * FROM livestreams WHERE id = ? AND user_id = ?", livestreamID, userID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livestreams: "+err.Error())
	}
	if len(ownedLivestreams) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "A streamer can't moderate livestreams that other streamers own")
	}

	rs, err := tx.NamedExecContext(ctx, "INSERT INTO ng_words(user_id, livestream_id, word, created_at) VALUES (:user_id, :livestream_id, :word, :created_at)", &NGWord{
		UserID:       int64(userID),
		LivestreamID: int64(livestreamID),
		Word:         req.NGWord,
		CreatedAt:    time.Now().Unix(),
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to insert new NG word: "+err.Error())
	}

	wordID, err := rs.LastInsertId()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get last inserted NG word id: "+err.Error())
	}

	var ngwords []*NGWord
	if err := tx.SelectContext(ctx, &ngwords, "SELECT * FROM ng_words WHERE livestream_id = ?", livestreamID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get NG words: "+err.Error())
	}

	var livecomments []*LivecommentModel
	if err := tx.SelectContext(ctx, &livecomments, "SELECT * FROM livecomments"); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get livecomments: "+err.Error())
	}

	var deleteLiveComentIDs []string
	for _, lc := range livecomments {
		for _, ng := range ngwords {
			if strings.Contains(lc.Comment, ng.Word) {
				deleteLiveComentIDs = append(deleteLiveComentIDs, strconv.FormatInt(lc.ID, 10))
				break
			}
		}
	}

	if len(deleteLiveComentIDs) > 0 {
		query := `
		DELETE FROM livecomments
		WHERE
		id IN (%s) AND
		livestream_id = ? 
		`
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(query, strings.Join(deleteLiveComentIDs, ",")), livestreamID); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to delete old livecomments that hit spams: "+err.Error())
		}

	}

	if err := tx.Commit(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to commit: "+err.Error())
	}

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"word_id": wordID,
	})
}

func fillLivecommentResponse(ctx context.Context, tx *sqlx.Tx, livecommentModel LivecommentModel) (Livecomment, error) {
	commentOwnerModel := UserModel{}
	if err := tx.GetContext(ctx, &commentOwnerModel, "SELECT * FROM users WHERE id = ?", livecommentModel.UserID); err != nil {
		return Livecomment{}, err
	}
	commentOwner, err := fillUserResponse(ctx, tx, commentOwnerModel)
	if err != nil {
		return Livecomment{}, err
	}

	livestreamModel := LivestreamModel{}
	if err := tx.GetContext(ctx, &livestreamModel, "SELECT * FROM livestreams WHERE id = ?", livecommentModel.LivestreamID); err != nil {
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

func fillLivecommentReportResponse(ctx context.Context, tx *sqlx.Tx, reportModel LivecommentReportModel) (LivecommentReport, error) {
	reporterModel := UserModel{}
	if err := tx.GetContext(ctx, &reporterModel, "SELECT * FROM users WHERE id = ?", reportModel.UserID); err != nil {
		return LivecommentReport{}, err
	}
	reporter, err := fillUserResponse(ctx, tx, reporterModel)
	if err != nil {
		return LivecommentReport{}, err
	}

	livecommentModel := LivecommentModel{}
	if err := tx.GetContext(ctx, &livecommentModel, "SELECT * FROM livecomments WHERE id = ?", reportModel.LivecommentID); err != nil {
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
