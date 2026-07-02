package study

import "github.com/3b391433/shanbay-cli/internal/api"

// Checkin records today's check-in when the day's task is complete — the web
// "去打卡" action. It reads the current state and only performs the check-in when
// it is available (status NOT_YET_CHECKIN); if already checked in or not yet
// eligible (today's task unfinished) it leaves the account untouched. This
// mirrors the web button, which the server gates via the check-in status.
//
// It returns the resulting state and whether this call performed the check-in.
func Checkin(c *api.Client) (state *api.Checkin, justChecked bool, err error) {
	st, err := c.CheckinInfo()
	if err != nil {
		return nil, false, err
	}
	if !st.NotYet() {
		return st, false, nil // already checked in, or task not finished yet
	}
	done, err := c.DoCheckin()
	if err != nil {
		return st, false, err
	}
	return done, true, nil
}
