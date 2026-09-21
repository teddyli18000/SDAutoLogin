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
