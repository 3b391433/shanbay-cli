package api

// Daily check-in ("打卡") under /uc/checkin. This mirrors the web "去打卡" button
// on web.shanbay.com/wordsweb/#/study/entry: it records that today's study is
// done and advances the streak. The web reads GET /uc/checkin to decide whether
// to show the button (status NOT_YET_CHECKIN) and POSTs /uc/checkin/logs to
// perform it; the server gates eligibility via the status field.

// Check-in status values (from the web bundle's checkin enum).
const (
	CheckinDone   = "HAVE_CHECKIN"    // already checked in today
	CheckinNotYet = "NOT_YET_CHECKIN" // eligible — the day's task is done, button shown
	// FORBIDDEN_CHECKIN_REQUIRED / FORBIDDEN_CHECKIN_ANY: not eligible yet
	// (today's task unfinished). Any non-NOT_YET/HAVE value means "can't check in".
)

// CheckinTask is one learn-activity row in the check-in status (背单词/阅读/精听…).
type CheckinTask struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	Operation    string `json:"operation"`
	Unit         string `json:"unit"`
	Num          int    `json:"num"`
	FinishStatus string `json:"finish_status"` // FINISHED / UNFINISHED
	Display      bool   `json:"display"`
	Required     bool   `json:"required"`
}

// Checkin is GET /uc/checkin (today's state) and the POST /uc/checkin/logs reply.
type Checkin struct {
	Date        string        `json:"date"`
	CheckinDays int           `json:"checkin_days_num"` // 累计打卡天数
	Status      string        `json:"status"`
	Note        string        `json:"note"`
	Tasks       []CheckinTask `json:"tasks"`
}

// Done reports whether today is already checked in.
func (c *Checkin) Done() bool { return c.Status == CheckinDone }

// NotYet reports whether check-in is available now (task done, not yet checked in).
func (c *Checkin) NotYet() bool { return c.Status == CheckinNotYet }

// CheckinInfo returns today's check-in state (GET /uc/checkin, plain JSON).
func (c *Client) CheckinInfo() (*Checkin, error) {
	var ck Checkin
	return &ck, c.getJSON("/uc/checkin", &ck)
}

// DoCheckin performs the daily check-in (POST /uc/checkin/logs, empty body) — the
// "去打卡" action. Call CheckinInfo first and only invoke this when NotYet() is
// true; the server rejects check-in while the day's task is unfinished.
func (c *Client) DoCheckin() (*Checkin, error) {
	var ck Checkin
	return &ck, c.postJSON("/uc/checkin/logs", struct{}{}, &ck)
}
