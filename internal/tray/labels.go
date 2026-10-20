package tray

import (
	"sdautologin/internal/i18n"
	"sdautologin/internal/service"
)

// stateLabel is the state name shown in the tray and its tooltip. The wording
// lives in the i18n table, keyed by the state code.
func stateLabel(st service.State) string {
	return i18n.L("state." + st.Code())
}

// updateLine is the tooltip line for a release the reader could move to, empty
// when there is nothing to say. It is here and not in each of the two trays, so
// the two cannot word it differently and one test covers both.
func updateLine(s service.Snapshot) string {
	if s.Update == nil || !s.Update.Newer {
		return ""
	}
	return "\n" + i18n.Lf("tray.updateAvailable", s.Update.Latest)
}
