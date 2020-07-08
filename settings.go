package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"syscall"
	"time"

	"github.com/jmoiron/sqlx/types"
	"github.com/labstack/echo"
)

type settings struct {
	AppRootURL       string   `json:"app.root_url"`
	AppLogoURL       string   `json:"app.logo_url"`
	AppFaviconURL    string   `json:"app.favicon_url"`
	AppFromEmail     string   `json:"app.from_email"`
	AppNotifyEmails  []string `json:"app.notify_emails"`
	AppBatchSize     int      `json:"app.batch_size"`
	AppConcurrency   int      `json:"app.concurrency"`
	AppMaxSendErrors int      `json:"app.max_send_errors"`
	AppMessageRate   int      `json:"app.message_rate"`

	Messengers []interface{} `json:"messengers"`

	PrivacyAllowBlacklist bool     `json:"privacy.allow_blacklist"`
	PrivacyAllowExport    bool     `json:"privacy.allow_export"`
	PrivacyAllowWipe      bool     `json:"privacy.allow_wipe"`
	PrivacyExportable     []string `json:"privacy.exportable"`

	SMTP []struct {
		Enabled       bool                `json:"enabled"`
		Host          string              `json:"host"`
		HelloHostname string              `json:"hello_hostname"`
		Port          int                 `json:"port"`
		AuthProtocol  string              `json:"auth_protocol"`
		Username      string              `json:"username"`
		Password      string              `json:"password"`
		EmailHeaders  []map[string]string `json:"email_headers"`
		MaxConns      int                 `json:"max_conns"`
		MaxMsgRetries int                 `json:"max_msg_retries"`
		IdleTimeout   string              `json:"idle_timeout"`
		WaitTimeout   string              `json:"wait_timeout"`
		TLSEnabled    bool                `json:"tls_enabled"`
		TLSSkipVerify bool                `json:"tls_skip_verify"`
	} `json:"smtp"`

	UploadProvider string `json:"upload.provider"`

	UploadFilesystemUploadPath string `json:"upload.filesystem.upload_path"`
	UploadFilesystemUploadURI  string `json:"upload.filesystem.upload_uri"`

	UploadS3AwsAccessKeyID     string `json:"upload.s3.aws_access_key_id"`
	UploadS3AwsDefaultRegion   string `json:"upload.s3.aws_default_region"`
	UploadS3AwsSecretAccessKey string `json:"upload.s3.aws_secret_access_key"`
	UploadS3Bucket             string `json:"upload.s3.bucket"`
	UploadS3BucketDomain       string `json:"upload.s3.bucket_domain"`
	UploadS3BucketPath         string `json:"upload.s3.bucket_path"`
	UploadS3BucketType         string `json:"upload.s3.bucket_type"`
	UploadS3Expiry             int    `json:"upload.s3.expiry"`
}

// handleGetSettings returns settings from the DB.
func handleGetSettings(c echo.Context) error {
	var (
		app = c.Get("app").(*App)
		out types.JSONText
	)

	if err := app.queries.GetSettings.Get(&out); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError,
			fmt.Sprintf("Error fetching settings: %s", pqErrMsg(err)))
	}

	// Unmarshall the settings and filter out sensitive fields.
	var s settings
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError,
			fmt.Sprintf("Error parsing settings: %v", err))
	}

	for i := 0; i < len(s.SMTP); i++ {
		s.SMTP[i].Password = ""
	}
	s.UploadS3AwsSecretAccessKey = ""

	return c.JSON(http.StatusOK, okResp{s})
}

// handleUpdateSettings returns settings from the DB.
func handleUpdateSettings(c echo.Context) error {
	var (
		app = c.Get("app").(*App)
		s   settings
	)

	// Unmarshal and marshal the fields once to sanitize the settings blob.
	if err := c.Bind(&s); err != nil {
		return err
	}

	b, err := json.Marshal(s)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError,
			fmt.Sprintf("Error encoding settings: %v", err))
	}

	// There should be at least one SMTP block that's enabled.
	has := false
	for _, s := range s.SMTP {
		if s.Enabled {
			has = true
			break
		}
	}
	if !has {
		return echo.NewHTTPError(http.StatusBadRequest,
			"At least one SMTP block should be enabled")
	}

	// Update the settings in the DB.
	if _, err := app.queries.UpdateSettings.Exec(b); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError,
			fmt.Sprintf("Error updating settings: %s", pqErrMsg(err)))
	}

	// If there are any active campaigns, don't do an auto reload and
	// warn the user on the frontend.
	if app.manager.HasRunningCampaigns() {
		app.Lock()
		app.needsRestart = true
		app.Unlock()

		return c.JSON(http.StatusOK, okResp{struct {
			NeedsRestart bool `json:"needs_restart"`
		}{true}})
	}

	// No running campaigns. Reload the app.
	go func() {
		<-time.After(time.Millisecond * 500)
		app.sigChan <- syscall.SIGHUP
	}()

	return c.JSON(http.StatusOK, okResp{true})
}
