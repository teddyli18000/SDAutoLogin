package i18n

// webText holds the wording of the settings page; init merges it into uiText, so
// the page is served from the same table as the tray.
var webText = map[string]text{
	"tab.general":  {"常规", "General"},
	"tab.advanced": {"高级", "Advanced"},
	"tab.about":    {"关于", "About"},

	"sub.accounts":     {"登录账号", "Accounts"},
	"sub.connectivity": {"连接检测", "Connectivity"},
	"sub.options":      {"运行选项", "Options"},

	// The login list, in priority order.
	"acct.order.hint": {
		"列表顺序就是优先级，从上往下依次尝试。",
		"The list order is the priority; entries are tried from the top down."},
	"acct.card":          {"账号 {0}", "Account {0}"},
	"acct.enabled":       {"启用", "Enabled"},
	"acct.active":        {"当前使用", "In use"},
	"acct.disabled":      {"已停用", "Disabled"},
	"acct.skipped":       {"{0} 后重试 · {1}", "retrying in {0} · {1}"},
	"acct.services":      {"服务类型（按尝试顺序）", "Service types (tried in this order)"},
	"acct.services.hint": {"不选则按校园网本身登录。", "With none picked, the campus network itself is used."},
	"acct.addService":    {"添加服务类型", "Add a service type"},
	"acct.custom":        {"自定义后缀…", "Custom suffix..."},
	"acct.custom.ph":     {"例如 @zgyd", "for example @zgyd"},
	"acct.custom.hint":   {"门户下拉框里没有的类型，例如 @zgyd", "A type missing from the portal's dropdown, e.g. @zgyd"},
	"acct.add":           {"添加账号", "Add account"},
	"acct.remove":        {"删除", "Remove"},
	"acct.up":            {"上移", "Move up"},
	"acct.down":          {"下移", "Move down"},
	"acct.drag":          {"拖动以调整优先级", "Drag to change the priority"},
	"acct.test":          {"测试", "Test"},
	"acct.testing":       {"测试中…", "Testing..."},
	"acct.test.dirty":    {"保存后才能测试", "Save first, then test"},
	"acct.testOk":        {"登录成功", "login accepted"},
	"acct.testOnline":    {"当前已在线，未提交登录", "already signed in, nothing submitted"},
	"acct.testFail":      {"被拒绝：{0}", "refused: {0}"},
	"acct.empty": {
		"还没有账号，请添加一个。",
		"No account yet, add one."},
	"acct.summary":     {"{0} 个账号 · 当前使用 {1}", "{0} accounts · in use: {1}"},
	"acct.summaryOnly": {"{0} 个账号", "{0} accounts"},
	"acct.allDisabled": {"所有账号都已停用，不会尝试登录。", "All accounts are disabled; nothing will be signed in."},

	// Shown on the card whose account the session is on, whenever there is
	// another service type of that entry to move it to.
	"acct.preferred.now": {
		"会话当前在 {0}，本账号的首选是 {1}。",
		"The session is on {0}; this account puts {1} first."},
	"acct.preferred.same": {
		"会话当前在 {0}，这也正是本账号的首选。",
		"The session is on {0}, this account's preferred service type."},
	// Shown when the portal would not sign in again to move a session that is up.
	// The line tells the reader what to do, since the program has stopped trying
	// and the second button is the only way out while a session is up.
	"acct.preferred.refused": {
		"需手动处理：自动切换到 {0} 被门户拒绝（{1}）。可点「重新登录」再试一次，或点「打开门户页面」注销当前会话后再试。",
		"Needs doing by hand: the move onto {0} was refused ({1}). Press \"Sign in again\" to try once more, or \"Open the portal\" to log the session out and try again."},
	"acct.preferred.choose": {"改为", "Change to"},
	"acct.preferred.btn":    {"重新登录", "Sign in again"},
	"acct.preferred.portal": {"打开门户页面", "Open the portal"},
	"acct.preferred.none": {
		"无法切换：会话已不在线，或这个服务类型不在该账号的列表里。",
		"Nothing to switch: the session is down, or that service type is not one this account lists."},
	"acct.preferred.moved": {"已用 {0} 重新登录。", "Signed in again as {0}."},
	"acct.preferred.failed": {
		"门户没有换：{0}。会话维持原样。",
		"The portal would not sign in again: {0}. The session stays as it is."},
	"acct.switch.dirty": {"保存后才能切换", "Save first, then switch"},

	"field.account":       {"账号", "Account"},
	"field.account.hint":  {"学号、用户名或邮箱均可", "Student id, user name or mail address"},
	"field.password":      {"密码", "Password"},
	"field.password.keep": {"已设置，留空表示不改动", "Set; leave it empty to keep it"},
	// Shown on a card whose password the server reads from the password file,
	// in place of the field, which is closed there.
	"field.password.fromFile":         {"密码来自 {0}", "Read from {0}"},
	"field.password.fromFile.unnamed": {"密码来自密码文件", "Read from the password file"},
	"field.show":                      {"显示", "Show"},
	"field.hide":                      {"隐藏", "Hide"},

	"field.rememberPassword":      {"记住密码（写入配置文件）", "Remember passwords (write them to the config file)"},
	"field.rememberPassword.hint": {"关闭后密码不写入配置文件；无法无人值守登录。", "With this off, passwords are not written to disk; unattended login is not possible."},
	"field.passwordFile":          {"密码文件", "Password file"},
	"field.passwordFile.hint": {
		"留空则密码仍存在 config.json 里。文件每行一条「账号:密码」；整份文件都没有冒号时，就是一个密码，归第一个账号。读到的密码不会再写回 config.json。",
		"Leave it empty to keep the passwords in config.json. The file holds one account:password per line, or a single password for the first entry if no line holds a colon. Passwords read from it are not written back to config.json."},
	"field.autoLogin":     {"掉线后自动登录", "Sign in automatically after a disconnection"},
	"field.autostart":     {"开机自动运行", "Run at startup"},
	"field.switchService": {"启动时把已在线的会话换成首选服务类型", "Move a session that is already up onto the first service type"},
	"field.switchService.hint": {
		"程序启动前就已登录的会话（例如手动登录的）如果不在账号的首选服务类型上，会重新登录一次换过去；关掉则保持不变，用账号卡片上的按钮手动切换。关掉「掉线后自动登录」时也不会自动切换。",
		"A session that was up before the program started - one signed in by hand, say - is signed in again when it is not on the first service type of its account. With this off it is left as it is, and the button on its card moves it by hand; nothing is switched on its own either while automatic login is off."},
	"field.checkUpdates": {"启动时检查新版本", "Look for a newer release"},
	"field.checkUpdates.hint": {
		"默认关闭。开启后每隔几小时问一次发布页；能自己替换自己的安装会自动升级，装成系统包或容器的只提示。",
		"Off by default. When on, the release page is asked every few hours. An install that can replace itself is updated; one that came from a package or an image is only told."},
	"field.updateChannel": {"更新通道", "Update channel"},
	"channel.stable":      {"正式版", "Stable"},
	"channel.prerelease":  {"尝鲜版（含预发布）", "Pre-release (includes them)"},
	"field.interval":      {"检测间隔（秒）", "Check interval (seconds)"},
	"field.interval.hint": {"最小 5 秒", "Minimum 5 seconds"},
	"field.checkInternet": {"同时探测外网可达性（仅用于状态提示）", "Also probe Internet reachability (status only)"},
	"field.probes":        {"连通性检测地址", "Reachability probe addresses"},
	"field.probes.hint":   {"任一地址正常返回，即认为外网可用", "The Internet counts as reachable when any of them answers normally"},
	"field.language":      {"界面语言", "Interface language"},

	"lang.auto": {"跟随系统", "Follow system"},
	"lang.zh":   {"简体中文", "Chinese (simplified)"},
	"lang.en":   {"English", "English"},

	// The service types the portal's own dropdown lists. The command line and
	// the log keep their English names; the page shows them in the interface
	// language.
	"op.campus": {"校园网", "Campus network"},
	"op.zgyd":   {"中国移动", "China Mobile"},
	"op.cucc":   {"中国联通", "China Unicom"},
	"op.ctc":    {"中国电信", "China Telecom"},

	"field.portalHome":    {"门户首页地址", "Portal home URL"},
	"field.apiEndpoint":   {"认证接口地址", "Authentication API URL"},
	"field.accountPrefix": {"账号前缀", "Account prefix"},
	"field.accountPrefix.hint": {"桌面浏览器登录固定为 “,0,”。",
		"Fixed to \",0,\" for a desktop browser login."},
	"field.httpTimeout": {"请求超时（秒）", "Request timeout (seconds)"},
	"field.jsVersion":   {"jsVersion 参数", "jsVersion parameter"},
	"field.verboseLog":  {"详细日志（记录每次检测的诊断信息）", "Verbose logging (per-check diagnostics)"},
	"advanced.note":     {"除非门户地址有变动，否则无需修改。", "Nothing here needs changing unless the portal address changed."},

	"status.state":      {"状态", "State"},
	"status.clientIp":   {"客户端 IP", "Client IP"},
	"status.account":    {"在线账号", "Signed in as"},
	"status.entry":      {"使用中的账号", "Account in use"},
	"status.since":      {"会话开始", "Session since"},
	"status.lastLogin":  {"上次自动登录", "Last auto login"},
	"status.internet":   {"外网", "Internet"},
	"status.checked":    {"上次检查", "Last check"},
	"status.checkedAgo": {"{0} 秒前", "{0}s ago"},
	"status.checkedMin": {"{0} 分钟前", "{0}m ago"},
	"status.internetOk": {"可达", "reachable"},
	"status.internetNo": {"不通", "unreachable"},
	"status.checkNow":   {"立即检测", "Check now"},
	"status.testLogin":  {"测试首个账号", "Test the first account"},
	// Shown while the portal would not move the session where the order of the
	// login list says it should be. The session is up, so nothing else in the
	// strip says there is anything to do.
	"status.attention": {"需处理", "needs attention"},
	"status.switchRefused": {
		"需手动处理：会话停在 {1}，未能切换到 {0}，请用下方账号卡片上的按钮",
		"Needs doing by hand: the session stays on {1} and could not move onto {0} - use the buttons on the account card below"},

	"btn.save":       {"保存", "Save"},
	"btn.reload":     {"重新载入配置", "Reload config"},
	"btn.openConfig": {"打开配置文件", "Open config file"},
	"btn.openLog":    {"打开日志", "Open log"},
	"btn.quit":       {"退出程序", "Quit"},
	"btn.addProbe":   {"添加地址", "Add address"},
	"btn.remove":     {"删除", "Remove"},
	"btn.ok":         {"确定", "OK"},
	"btn.cancel":     {"取消", "Cancel"},

	"test.title":   {"测试结果", "Test result"},
	"test.raw":     {"原始应答", "Raw reply"},
	"test.retcode": {"ret_code", "ret_code"},
	"test.entry":   {"提交的账号", "Submitted"},

	"about.version":     {"版本", "Version"},
	"about.platform":    {"运行平台", "Platform"},
	"about.repo":        {"项目主页", "Project"},
	"about.license":     {"许可", "License"},
	"about.configPath":  {"配置文件", "Config file"},
	"about.logPath":     {"日志文件", "Log file"},
	"about.logTail":     {"最近日志", "Recent log"},
	"about.update":      {"新版本", "Newer release"},
	"about.update.idle": {"尚未检查", "Not checked yet"},
	"btn.checkUpdate":   {"检查更新", "Check for updates"},
	"err.noUpdater":     {"本次运行没有发布页可问", "This run has no release page to ask"},
	"about.security": {"设置页默认只监听本机回环地址，并使用一次性令牌鉴权。",
		"The settings page listens on loopback by default and requires a one time token."},

	"msg.saved":       {"已保存，并立即重新检测了一次。", "Saved, and one check was run immediately."},
	"msg.reloaded":    {"已重新载入配置文件，未保存的修改已丢弃。", "Reloaded the config file; unsaved changes were discarded."},
	"msg.autostart":   {"开机自启设置已更新。", "The startup setting has been updated."},
	"msg.stale":       {"设置页已失效，请从托盘重新打开。", "This page is no longer valid, open it again from the tray."},
	"msg.saving":      {"正在保存…", "Saving..."},
	"msg.unsaved":     {"有未保存的修改", "Unsaved changes"},
	"msg.confirmQuit": {"确定退出程序吗？退出不会注销校园网登录。", "Quit the application? This does not log the campus session out."},
	"msg.confirmReload": {
		"重新载入会丢弃未保存的修改。继续吗？",
		"Reloading discards unsaved changes. Continue?"},
	"msg.noStatus": {"尚无检测结果", "No check has finished yet"},
	"msg.quitDone": {"程序已退出，此页面可以关闭。", "The application has quit. This page can be closed."},
	"msg.confirmRemove": {
		"删除账号 {0}？该账号的密码也会从配置里移除。",
		"Remove account {0}? Its password is removed from the configuration as well."},

	"wizard.title":  {"首次使用，先完成基本设置", "First run: finish the basic setup"},
	"wizard.step1":  {"1. 选择界面语言", "1. Choose the interface language"},
	"wizard.step2":  {"2. 填写账号和密码", "2. Enter the account and password"},
	"wizard.step3":  {"3. 选择服务类型", "3. Pick the service type"},
	"wizard.finish": {"完成并开始自动登录", "Finish and start auto login"},
	// Skipping leaves the settings page, which is behind the wizard, as the way
	// in: not every machine is set up on the spot.
	"wizard.skip": {"以后再说", "Not now"},

	"err.accountsRequired":  {"至少需要一个账号", "At least one account is required"},
	"err.accountRequired":   {"账号不能为空", "Account is required"},
	"err.passwordRequired":  {"密码不能为空", "Password is required"},
	"err.passwordNotInFile": {"密码从文件读，文件里没有这个账号", "The password is read from a file, and that file does not name this account"},
	"err.noUsableEntry":     {"这个账号没有填写密码，或者已被停用", "This account has no password, or is disabled"},
	"err.intervalTooSmall":  {"检测间隔至少 5 秒", "The interval must be at least 5 seconds"},
	"err.timeoutTooSmall":   {"请求超时至少 2 秒", "The timeout must be at least 2 seconds"},
	"err.urlInvalid":        {"必须以 http:// 或 https:// 开头", "Must start with http:// or https://"},
	"err.probeInvalid":      {"地址无效", "Invalid address"},
	"err.badRequest":        {"请求格式错误", "Malformed request"},
	"err.saveFailed":        {"保存失败，请查看日志", "Could not save, see the log"},
	"err.loadFailed":        {"读取配置失败", "Could not read the config"},
	"err.unknownAction":     {"未知操作", "Unknown action"},
	"err.openPortal":        {"无法打开门户页面，请查看日志", "Could not open the portal page, see the log"},

	"retcode.0":  {"登录成功", "login accepted"},
	"retcode.1":  {"账号或密码不对", "wrong account or password"},
	"retcode.2":  {"该 IP 已经在线", "this IP is already online"},
	"retcode.3":  {"系统忙，请稍后再试", "portal busy, try again later"},
	"retcode.4":  {"发生未知错误", "unknown portal error"},
	"retcode.5":  {"REQ_CHALLENGE 失败", "REQ_CHALLENGE failed"},
	"retcode.6":  {"REQ_CHALLENGE 超时", "REQ_CHALLENGE timeout"},
	"retcode.7":  {"认证失败", "authentication failed"},
	"retcode.8":  {"认证超时", "authentication timeout"},
	"retcode.9":  {"下线失败", "logout failed"},
	"retcode.10": {"下线超时", "logout timeout"},
	"retcode.11": {"其他错误", "other error"},
}
