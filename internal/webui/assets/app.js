"use strict";

/* The address the page was opened with carried a token, and the server
   exchanged it for this cookie before serving the page, so what is left in the
   address bar carries nothing. The page repeats the cookie in a header of its
   own on every request; a request that changes something is only accepted with
   both. */
const TOKEN_COOKIE = "sdauth";

function readToken() {
  const prefix = TOKEN_COOKIE + "=";
  const found = document.cookie.split("; ").find((part) => part.startsWith(prefix));
  return found ? found.slice(prefix.length) : "";
}

const token = readToken();

const state = {
  i18n: {},
  config: null,
  status: {},
  operators: [],
  errors: {},
  dirty: false,
  configured: true,
};

const $ = (id) => document.getElementById(id);

/* Every decision this page makes that a reader could later ask about is written
   to the browser's console, so a report of "the button did nothing" can be read
   back in the order it happened. The prefix is what to filter on. Nothing secret
   goes through here: whether a password is there is said, never the password. */
function note(what, detail) {
  if (detail === undefined) console.log("[sdautologin] " + what);
  else console.log("[sdautologin] " + what, detail);
}

function t(key, ...args) {
  let out = state.i18n[key] || key;
  args.forEach((a, i) => { out = out.split("{" + i + "}").join(a); });
  return out;
}

async function api(path, options) {
  const opts = options || {};
  opts.headers = Object.assign({ "X-Auth-Token": token }, opts.headers || {});
  if (opts.body) {
    opts.headers["Content-Type"] = "application/json";
  }
  let resp;
  try {
    resp = await fetch(path, opts);
  } catch (err) {
    /* The program quit, or the idle service shut itself down. Callers must
       still get an answer instead of an exception. */
    return { ok: false, status: 0, data: {}, unreachable: true };
  }
  const data = await resp.json().catch(() => ({}));
  return { ok: resp.ok, status: resp.status, data };
}

/* ---------- building blocks ---------- */

function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  Object.entries(attrs || {}).forEach(([key, value]) => {
    if (value === null || value === undefined || value === false) return;
    if (key === "class") node.className = value;
    else if (key === "text") node.textContent = value;
    else if (key === "dataset") Object.assign(node.dataset, value);
    else if (key === "value") node.value = value;
    else if (key.startsWith("on")) node.addEventListener(key.slice(2), value);
    else node.setAttribute(key, value === true ? "" : value);
  });
  children.flat().forEach((child) => {
    if (child !== null && child !== undefined) node.append(child);
  });
  return node;
}

function button(label, extra) {
  return el("button", { type: "button", class: "btn " + (extra || ""), text: label });
}

function iconButton(act, glyph, label, extra) {
  return el("button", {
    type: "button",
    class: "icon " + (extra || ""),
    title: label,
    "aria-label": label,
    dataset: { act },
    text: glyph,
  });
}

/* The service types are named in the language of the interface here, while the
   log and the command line keep the English names the portal's own dropdown
   uses. A suffix the dropdown does not list is shown as it is. */
const OPERATOR_KEYS = { "": "op.campus", "@zgyd": "op.zgyd", "@cucc": "op.cucc", "@ctc": "op.ctc" };

function operatorName(suffix) {
  if (OPERATOR_KEYS[suffix]) return t(OPERATOR_KEYS[suffix]);
  const known = state.operators.find((op) => op.Suffix === suffix);
  return known ? known.Name : suffix;
}

/* ---------- text and language ---------- */

function applyI18n() {
  document.querySelectorAll("[data-i18n]").forEach((node) => {
    node.textContent = t(node.dataset.i18n);
  });
  document.querySelectorAll("[data-i18n-ph]").forEach((node) => {
    node.placeholder = t(node.dataset.i18nPh);
  });
  document.title = t("app.name");
}

function applyLang(code) {
  document.documentElement.lang = code === "zh" ? "zh-CN" : "en";
}

/* The string table arrives with the bootstrap response, so a page whose
   bootstrap failed has to take the language from the browser instead. Both
   wordings live here rather than in the table, which this page cannot reach,
   and they say where to open the page again rather than naming a token: there
   is nowhere on this page to type one. */
function bootFailedText() {
  if ((navigator.language || "").toLowerCase().startsWith("zh")) {
    return "设置页已失效，请从托盘重新打开，或打开日志里最新的那个地址。";
  }
  return "This page is no longer valid, open it again from the tray, or from the newest address in the log.";
}

/* The autostart entry lives outside config.json, it is toggled on its own. */
function applyAutostart(on) {
  if (typeof on === "boolean") $("f-autostart").checked = on;
}

/* ---------- the login list ---------- */

function applyConfig(cfg) {
  state.config = cfg;
  $("f-interval").value = cfg.checkIntervalSeconds;
  $("f-timeout").value = cfg.httpTimeoutSeconds;
  $("f-portal").value = cfg.portalBaseUrl || "";
  $("f-eportal").value = cfg.eportalUrl || "";
  $("f-prefix").value = cfg.accountPrefix || "";
  $("f-jsversion").value = cfg.jsVersion || "";
  $("f-remember").checked = !!cfg.rememberPassword;
  $("f-autologin").checked = !!cfg.autoLogin;
  $("f-internet").checked = !!cfg.checkInternet;
  $("f-verbose").checked = !!cfg.verboseLog;
  $("f-preferred").checked = !!cfg.switchToPreferredService;
  $("f-language").value = cfg.language || "auto";
  renderProbes(cfg.probeUrls || []);
  renderAccounts(cfg.accounts || []);
}

function renderAccounts(accounts) {
  const list = $("accountList");
  list.innerHTML = "";
  accounts.forEach((entry, index) => list.append(accountCard(entry, index)));
  $("acctEmpty").hidden = accounts.length > 0;
  $("acctWarn").hidden = !(accounts.length > 0 && accounts.every((entry) => entry.disabled));
  applyErrors();
  applyStatusBadges(state.status);
  updateEntryActions();
}

// The test button and the switch both ask the running program about the list it
// is running with, which is keyed by position, so an entry just moved or edited
// is not what the program would use yet. They therefore wait for a save.
function updateEntryActions() {
  Array.from(document.querySelectorAll('#accountList [data-act="test"]')).forEach((node) => {
    const index = Number(node.closest(".account").dataset.index);
    const entry = state.config && state.config.accounts && state.config.accounts[index];
    const off = !!(entry && entry.disabled);
    node.disabled = off || state.dirty;
    node.title = off ? t("acct.disabled") : state.dirty ? t("acct.test.dirty") : "";
  });
  Array.from(document.querySelectorAll('#accountList [data-act="switchService"], #accountList .preferred-isp'))
    .forEach((node) => {
      node.disabled = state.dirty;
      node.title = state.dirty ? t("acct.switch.dirty") : "";
    });
}

function accountCard(entry, index) {
  const isps = entry.isps && entry.isps.length ? entry.isps : [""];

  const number = el("span", { class: "account-no", text: String(index + 1) });
  const grip = el("span", { class: "grip", title: t("acct.drag"), "aria-hidden": "true", text: "⠿" });
  const card = el("article", { class: "account", dataset: { index: String(index) } });

  const head = el("div", { class: "account-head" },
    grip,
    number,
    el("span", {
      class: "account-title",
      text: entry.username || t("acct.card", index + 1),
    }),
    // The three states an entry can be in besides usable: the session is on it,
    // the portal refused it for a while, or it is switched off. applyStatusBadges
    // fills in what they say.
    el("span", { class: "badge active", text: t("acct.active"), hidden: true }),
    el("span", { class: "badge skipped", hidden: true }),
    el("span", { class: "badge off", text: t("acct.disabled"), hidden: !entry.disabled }),
    el("div", { class: "spacer" }),
    el("label", { class: "switch" },
      el("input", {
        type: "checkbox",
        dataset: { field: "enabled" },
        checked: !entry.disabled,
        onchange: markDirty,
      }),
      el("span", { text: t("acct.enabled") })),
    el("div", { class: "account-tools" },
      iconButton("up", "↑", t("acct.up")),
      iconButton("down", "↓", t("acct.down")),
      iconButton("remove", "×", t("acct.remove"), "danger")));

  const usernameID = "acct-" + index + "-username";
  const passwordID = "acct-" + index + "-password";

  const username = el("input", {
    id: usernameID,
    autocomplete: "off",
    spellcheck: "false",
    value: entry.username || "",
    oninput: markDirty,
    dataset: { field: "username" },
  });

  // The password is the one field the page is never given: an entry that has one
  // says so instead, and the field is left empty for the reader to replace it
  // with. Empty is what "keep the stored one" looks like in the request the page
  // sends back.
  //
  // What the reader has typed is put back as the card is rebuilt, because a card
  // is rebuilt from what is on the page every time the list changes: a password
  // typed into a card and then lost to a drag, a language change or a refused
  // save was reported back as a missing one, on an account the reader had just
  // filled in. The stored password is still never sent to the page, so an entry
  // that comes from the program arrives with this empty and says so instead.
  const password = el("input", {
    id: passwordID,
    type: "password",
    autocomplete: "off",
    value: entry.password || "",
    placeholder: entry.hasPassword ? t("field.password.keep") : t("field.password"),
    oninput: markDirty,
    dataset: { field: "password" },
  });

  const show = button(t("field.show"), "ghost");
  show.dataset.act = "show";

  const chips = el("div", { class: "chips", dataset: { chips: "" } },
    isps.map((suffix) => chip(suffix)));

  const picker = servicePicker(isps);
  const custom = el("input", {
    class: "custom-suffix",
    hidden: true,
    spellcheck: "false",
    placeholder: t("acct.custom.ph"),
    dataset: { act: "custom" },
    onkeydown: (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        addCustomSuffix(card);
      }
    },
    onblur: (event) => {
      if (event.target.value.trim() !== "") addCustomSuffix(card);
    },
  });

  const test = button(t("acct.test"), "ghost");
  test.dataset.act = "test";
  const testResult = el("div", { class: "test-result", hidden: true });

  // Shown only on the card the session is on, and only while the entry lists
  // another service type to move it to; applyStatusBadges decides both from the
  // status it is handed, and fills in the select and the line.
  const switchButton = button(t("acct.preferred.btn"), "ghost");
  switchButton.dataset.act = "switchService";
  const preferred = el("div", { class: "account-preferred", hidden: true },
    el("p", { class: "hint preferred-line" }),
    el("div", { class: "preferred-actions" },
      el("span", { class: "preferred-target", text: t("acct.preferred.choose") }),
      el("select", { class: "preferred-isp" }),
      switchButton));

  const body = el("div", { class: "account-body" },
    row(usernameID, t("field.account"), username, t("field.account.hint"), "accounts." + index + ".username"),
    row(passwordID, t("field.password"), el("div", { class: "input-row" }, password, show),
      null, "accounts." + index + ".password"),
    row(null, t("acct.services"), el("div", { class: "services" },
      chips,
      el("div", { class: "service-add" }, picker, custom),
      el("p", { class: "hint", text: t("acct.services.hint") })), null, null),
    el("div", { class: "account-foot" }, test, preferred, testResult));

  card.append(head, body);
  return card;
}

function row(labelID, label, control, hint, errorKey) {
  return el("div", { class: "row" },
    el("label", { class: "row-label", for: labelID, text: label }),
    el("div", { class: "row-body" },
      control,
      hint ? el("p", { class: "hint", text: hint }) : null,
      errorKey ? el("p", { class: "error", dataset: { error: errorKey } }) : null));
}

// A chip is one service type of one account. They are dragged, or moved with
// the arrow keys while one has the focus, because their order is the order the
// portal is asked in.
function chip(suffix) {
  return el("span", {
    class: "chip",
    draggable: "true",
    tabindex: "0",
    title: t("acct.services"),
    dataset: { suffix },
  },
    el("span", { class: "chip-name", text: operatorName(suffix) }),
    el("button", {
      type: "button",
      class: "chip-x",
      title: t("btn.remove"),
      "aria-label": t("btn.remove"),
      dataset: { act: "unservice" },
      text: "×",
    }));
}

function servicePicker(chosen) {
  const select = el("select", { class: "picker", dataset: { act: "service-picker" } });
  select.append(el("option", { value: "__add", text: t("acct.addService") }));
  state.operators.forEach((op) => {
    if (chosen.includes(op.Suffix)) return;
    select.append(el("option", { value: op.Suffix, text: operatorName(op.Suffix) }));
  });
  select.append(el("option", { value: "__custom", text: t("acct.custom") }));
  return select;
}

/* ---------- reading and writing the list ---------- */

function collect() {
  const accounts = Array.from($("accountList").children).map((card) => ({
    username: card.querySelector('[data-field="username"]').value.trim(),
    password: card.querySelector('[data-field="password"]').value,
    disabled: !card.querySelector('[data-field="enabled"]').checked,
    isps: Array.from(card.querySelectorAll(".chip")).map((node) => node.dataset.suffix),
  }));
  return {
    accounts,
    rememberPassword: $("f-remember").checked,
    portalBaseUrl: $("f-portal").value.trim(),
    eportalUrl: $("f-eportal").value.trim(),
    accountPrefix: $("f-prefix").value,
    checkIntervalSeconds: parseInt($("f-interval").value, 10) || 0,
    httpTimeoutSeconds: parseInt($("f-timeout").value, 10) || 0,
    autoLogin: $("f-autologin").checked,
    switchToPreferredService: $("f-preferred").checked,
    checkInternet: $("f-internet").checked,
    verboseLog: $("f-verbose").checked,
    probeUrls: readProbes(),
    jsVersion: $("f-jsversion").value.trim(),
    language: $("f-language").value,
  };
}

/* accountsBrief is the list as a console line: the name of each entry and whether
   it carries a password. It is what a save writes into the console, so a refused
   save can be read back without the passwords ever being written down. */
function accountsBrief(accounts) {
  return accounts.map((entry, i) =>
    "#" + (i + 1) + " " + (entry.username || "(no account)") +
    (entry.password ? " password=set" : " password=empty"));
}

/* redraw rebuilds the list from the values read out of the DOM; every change to
   the order, the services or the account count ends here. */
function redraw(accounts) {
  state.config.accounts = accounts;
  renderAccounts(accounts);
  markDirty();
}

function addAccount() {
  const accounts = collect().accounts;
  accounts.push({ username: "", password: "", isps: [""] });
  redraw(accounts);
  const last = $("accountList").lastElementChild;
  if (last) last.querySelector('[data-field="username"]').focus();
}

function removeAccount(index) {
  const accounts = collect().accounts;
  confirmDialog(t("msg.confirmRemove", accounts[index].username || t("acct.card", index + 1)))
    .then((yes) => {
      if (!yes) return;
      accounts.splice(index, 1);
      state.status = Object.assign({}, state.status, { entryIndex: -1, skipped: [] });
      redraw(accounts);
    });
}

// moveAccount swaps one entry with its neighbour; the position in this list is
// the priority.
function moveAccount(index, delta) {
  const accounts = collect().accounts;
  const to = index + delta;
  if (to < 0 || to >= accounts.length) return;
  const moved = accounts.splice(index, 1)[0];
  accounts.splice(to, 0, moved);
  state.status = Object.assign({}, state.status, { entryIndex: -1, skipped: [] });
  redraw(accounts);
}

function renumber() {
  Array.from($("accountList").children).forEach((card, i) => {
    const badge = card.querySelector(".account-no");
    if (badge) badge.textContent = String(i + 1);
  });
}

function addService(card, suffix) {
  const chips = card.querySelector("[data-chips]");
  const chosen = Array.from(chips.children).map((node) => node.dataset.suffix);
  if (chosen.includes(suffix)) return;
  chips.append(chip(suffix));
  markDirty();
}

function addCustomSuffix(card) {
  const input = card.querySelector('[data-act="custom"]');
  const suffix = input.value.trim();
  input.value = "";
  input.hidden = true;
  if (suffix === "") return;
  addService(card, suffix);
  // The picker was built before this suffix existed, so it is rebuilt to drop
  // the suffix that was just added.
  refreshPicker(card);
}

function refreshPicker(card) {
  const chosen = Array.from(card.querySelectorAll(".chip")).map((node) => node.dataset.suffix);
  const old = card.querySelector('[data-act="service-picker"]');
  old.replaceWith(servicePicker(chosen));
  // A suffix that is already there is dropped from the picker, so a select that
  // was showing it has to go back to the placeholder.
  const picker = card.querySelector('[data-act="service-picker"]');
  picker.value = "__add";
}

/* ---------- status ---------- */

function entryLabel(index, usedSuffix) {
  const cfg = state.config;
  if (!cfg || !cfg.accounts || index < 0 || index >= cfg.accounts.length) return "-";
  const entry = cfg.accounts[index];
  const all = entry.isps && entry.isps.length ? entry.isps : [""];
  const isps = usedSuffix === undefined ? all : [usedSuffix];
  return (entry.username || t("acct.card", index + 1)) + " (" + isps.map(operatorName).join(", ") + ")";
}

// activeEntryLabel names the entry the session is on, with the one service type
// it is on rather than every type the entry may be used with. Which one that is
// comes from the program, which reads it off the account the portal echoes: a
// suffix the echo does not carry is one the login did not send, which is how a
// session on the campus network itself is told apart from one on a carrier.
function activeEntryLabel(st) {
  if (!st || typeof st.entryIndex !== "number" || st.entryIndex < 0) return "-";
  const cfg = state.config;
  if (!cfg || !cfg.accounts || st.entryIndex >= cfg.accounts.length) return "-";
  // The service type is known only while a session has been read, which is the
  // online state: a session that is gone is not on any of them, and the entry is
  // named with the types it may be used with instead. So is a status that does
  // not carry a service type at all, which is what the login self test answers.
  if (st.state === "online" && typeof st.usedIsp === "string") {
    return entryLabel(st.entryIndex, st.usedIsp);
  }
  return entryLabel(st.entryIndex);
}

function formatWait(seconds) {
  if (seconds < 60) return seconds + "s";
  return Math.floor(seconds / 60) + ":" + String(seconds % 60).padStart(2, "0");
}

function applyStatusBadges(st) {
  if (!st) return;
  Array.from($("accountList").children).forEach((card) => {
    const index = Number(card.dataset.index);
    const active = card.querySelector(".badge.active");
    const skipped = card.querySelector(".badge.skipped");
    if (active) {
      active.hidden = st.entryIndex !== index;
      active.textContent = t("acct.active");
    }
    if (skipped) {
      const why = (st.skipped || []).find((item) => item.index === index);
      skipped.hidden = !why;
      if (why) {
        // The portal's answer is localised by its ret_code, and one this program
        // has never seen is shown as the portal wrote it.
        const reason = state.i18n["retcode." + why.retCode] || why.message;
        skipped.textContent = t("acct.skipped", formatWait(why.retryInS), reason);
      }
    }
    // The card of the account the session is on: where the session is, where the
    // entry says it should be, and the way to move it there. It is shown
    // whenever that entry lists another service type, and the line says whether
    // the program's own attempt was refused, which is the case a reader cannot
    // see anywhere else: the session is up and working.
    renderPreferred(card, st, index);
  });
  updateSummary();
}

// renderPreferred fills in the move-the-session block of one card.
function renderPreferred(card, st, index) {
  const block = card.querySelector(".account-preferred");
  if (!block) return;
  // Only the card of the entry the session is on, and only while it is up: the
  // block is about a session that exists, and every other card's service types
  // have nothing to do with where that session is.
  const targets = st.state === "online" && st.entryIndex === index ? switchTargets(st, index) : [];
  block.hidden = targets.length === 0;
  if (targets.length === 0) return;

  const select = block.querySelector(".preferred-isp");
  // What the reader picked is kept while it is still one of the ways out: the
  // status is refreshed every few seconds, and the list is rebuilt with it. An
  // empty value is the campus network itself, which is a way out like any other,
  // so the first render is told apart by the options the select does not have
  // yet rather than by an empty value.
  const picked = select.options.length ? select.value : null;
  select.replaceChildren(...targets.map((suffix) =>
    el("option", { value: suffix, text: operatorName(suffix) })));
  const wanted = st.preferredIsp || "";
  select.value = picked !== null && targets.includes(picked) ? picked
    : targets.includes(wanted) ? wanted
      : targets[0];

  const line = block.querySelector(".preferred-line");
  const refused = st.switchRefused;
  line.classList.toggle("attention", !!refused);
  if (refused) {
    // The portal's answer is localised by its ret_code, the way the login self
    // test localises it.
    const reason = state.i18n["retcode." + (refused.retCode || "")] || refused.message || "";
    line.textContent = t("acct.preferred.refused", operatorName(select.value), reason);
    return;
  }
  const used = st.usedIsp || "";
  line.textContent = used === wanted
    ? t("acct.preferred.same", operatorName(used))
    : t("acct.preferred.now", operatorName(used), operatorName(wanted));
}

// switchTargets is the service types of one entry that the session is not on,
// which are the ones it can be moved to. The one it is on is left out: a login
// as that would ask the portal for what it already has.
function switchTargets(st, index) {
  const cfg = state.config;
  if (!cfg || !cfg.accounts || index < 0 || index >= cfg.accounts.length) return [];
  const entry = cfg.accounts[index];
  const isps = entry.isps && entry.isps.length ? entry.isps : [""];
  const used = typeof st.usedIsp === "string" ? st.usedIsp : "";
  return isps.filter((suffix) => suffix !== used);
}

function updateSummary() {
  const count = $("accountList").children.length;
  const label = activeEntryLabel(state.status);
  $("acctSummary").textContent = label === "-"
    ? t("acct.summaryOnly", count)
    : t("acct.summary", count, label);
}

function renderStatus(st) {
  state.status = st || {};
  const code = st.state || "checking";
  $("statePill").dataset.state = code;
  // A session that is up and that the program could not move where the order of
  // the login list says it should be needs a reader, and nothing else in the
  // strip says so: the state is Online, which reads as "nothing to do". The pill
  // carries the words and the strip carries the sentence, so the news is where
  // the state is looked at as well as beside the account it is about.
  const refused = st.switchRefused;
  $("statePill").classList.toggle("attention", !!refused);
  // The pill carries the state and, once something is signed in, who it is
  // signed in as: the account and the service type the session is on. That is
  // the question the strip of readings answers, and the strip is a row lower
  // than it needs to be for the one reading looked at most.
  const stateWord = state.i18n["state." + code] ? t("state." + code) : t("msg.noStatus");
  const who = activeEntryLabel(st);
  let pill = who === "-" ? stateWord : stateWord + " · " + who;
  if (refused) pill += " · " + t("status.attention");
  $("stateText").textContent = pill;
  $("stEntry").textContent = activeEntryLabel(st);
  $("stClientIp").textContent = st.clientIp || "-";
  $("stAccount").textContent = st.account || "-";
  $("stSince").textContent = st.onlineSince || "-";
  $("stLastLogin").textContent = st.lastLoginAt || "-";
  $("stInternet").textContent = st.internetChecked
    ? (st.internetOk ? t("status.internetOk") : t("status.internetNo"))
    : "-";
  $("statusNotice").hidden = !refused;
  if (refused) {
    $("statusNotice").textContent = t("status.switchRefused",
      operatorName(st.preferredIsp || ""), operatorName(st.usedIsp || ""));
  }
  applyStatusBadges(state.status);
}

/* ---------- errors and toasts ---------- */

function applyErrors() {
  document.querySelectorAll("[data-error]").forEach((node) => { node.textContent = ""; });
  document.querySelectorAll(".invalid").forEach((node) => node.classList.remove("invalid"));

  Object.entries(state.errors).forEach(([field, key]) => {
    const slot = document.querySelector('[data-error="' + field + '"]');
    if (slot) slot.textContent = t(key);

    const entry = field.match(/^accounts\.(\d+)\.(username|password)$/);
    if (entry) {
      const card = document.querySelector('.account[data-index="' + entry[1] + '"]');
      const input = card && card.querySelector('[data-field="' + entry[2] + '"]');
      if (input) input.classList.add("invalid");
      return;
    }
    const input = $("f-" + field);
    if (input) input.classList.add("invalid");
  });

  const generic = state.errors["_"];
  if (generic) toast(t(generic), true);

  // The wizard lies over the whole page, so a message that belongs to a field
  // underneath it cannot be seen: while it is open, the wizard carries the first
  // one itself. Without this a save the server refused looks like nothing
  // happened at all, which is what the first run does when its account has not
  // been filled in.
  if (!$("wizard").hidden) {
    const first = Object.values(state.errors)[0];
    const slot = document.querySelector('[data-error="wizard"]');
    if (first && slot) slot.textContent = t(first);
  }
}

let toastTimer = null;
function toast(text, isError) {
  const node = $("toast");
  node.textContent = text;
  node.classList.toggle("bad", !!isError);
  node.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { node.hidden = true; }, 6000);
}

function markDirty() {
  state.dirty = true;
  $("dirtyBadge").hidden = false;
  updateEntryActions();
}

/* ---------- dialogs ---------- */

let confirmResolve = null;

function confirmDialog(text) {
  $("confirmText").textContent = text;
  $("confirm").hidden = false;
  $("confirmYes").focus();
  return new Promise((resolve) => { confirmResolve = resolve; });
}

function closeConfirm(answer) {
  if ($("confirm").hidden) return;
  $("confirm").hidden = true;
  if (confirmResolve) {
    confirmResolve(answer);
    confirmResolve = null;
  }
}

/* ---------- saving ---------- */

async function submit() {
  const btn = $("btnSave");
  btn.disabled = true;
  btn.textContent = t("msg.saving");
  const body = collect();
  note("saving " + body.accounts.length + " account(s)", accountsBrief(body.accounts));
  const res = await api("/api/config", { method: "POST", body: JSON.stringify(body) });
  btn.disabled = false;
  btn.textContent = t("btn.save");
  if (res.unreachable) {
    note("the save did not reach the program: the page's session is gone");
    toast(t("msg.stale"), true);
    return false;
  }
  if (!res.ok || !res.data.ok) {
    state.errors = res.data.errors || { _: "err.saveFailed" };
    note("the save was refused", state.errors);
    renderAccounts(collect().accounts);
    return false;
  }
  note("the configuration was saved");
  state.errors = {};
  state.dirty = false;
  applyConfig(res.data.config);
  $("dirtyBadge").hidden = true;
  toast(t("msg.saved"));
  state.configured = true;
  $("wizard").hidden = true;
  refreshStatus();
  return true;
}

let warnedStale = false;

async function refreshStatus() {
  const res = await api("/api/status");
  if (res.unreachable || !res.ok) {
    // Both mean this page's session is gone: the program quit, or the settings
    // service shut itself down while the tab was idle, which is what a browser
    // does to a tab in the background. A refusal used to be passed over in
    // silence, leaving a page that kept showing the numbers it last saw and
    // could change nothing; saying it once is the whole remedy, since the way
    // back is to open the page again from the tray.
    if (!warnedStale) {
      warnedStale = true;
      // Said once per page, in the console as well as on screen: what the reader
      // sees is a page that has stopped answering, and the line says which of the
      // two it was.
      note("this page's session is gone: the status request was refused, the program has quit or its settings service has shut down");
      toast(t("msg.stale"), true);
    }
    return;
  }
  warnedStale = false;
  if (res.data.status) renderStatus(res.data.status);
  applyAutostart(res.data.autostart);
}

/* ---------- the login self test ---------- */

async function testEntry(index, buttonNode) {
  if (buttonNode) {
    buttonNode.disabled = true;
    buttonNode.textContent = t("acct.testing");
  }
  const res = await api("/api/action", {
    method: "POST",
    body: JSON.stringify({ action: "testLogin", index: index }),
  });
  if (buttonNode) {
    buttonNode.disabled = false;
    buttonNode.textContent = t("acct.test");
  }
  if (res.unreachable) {
    toast(t("msg.stale"), true);
    return;
  }
  if (!res.ok) {
    toast(t("err.noUsableEntry"), true);
    return;
  }
  const card = document.querySelector('.account[data-index="' + index + '"]');
  if (!card) return;
  const box = card.querySelector(".test-result");
  const retCode = res.data.result.retCode || "";
  const message = state.i18n["retcode." + retCode] || res.data.result.message || "";
  const accepted = !!res.data.result.success;
  const alreadyOnline = !!res.data.alreadyOnline;
  const verdict = alreadyOnline
    ? t("acct.testOnline")
    : accepted ? t("acct.testOk") : t("acct.testFail", message);

  box.hidden = false;
  box.className = "test-result " + (accepted || alreadyOnline ? "ok" : "bad");
  box.replaceChildren(el("div", { class: "test-line" },
    el("span", { class: "test-entry", text: entryLabel(index) }),
    el("span", { class: "test-verdict", text: verdict })));
  // Nothing was sent in that case, so there is no reply to show.
  if (!alreadyOnline) {
    box.append(el("details", {},
      el("summary", { text: t("test.raw") + " (" + t("test.retcode") + " " + (retCode === "" ? "-" : retCode) + ")" }),
      el("pre", { text: (res.data.result.raw || "").trim() })));
  }
}

/* ---------- the service type the session is on ---------- */

/* switchService asks the program to sign the session in again with the service
   type picked on the card, which replaces the session the portal is holding: one
   session per client address is what the portal allows, so that is the only way
   onto another one. The program says whether there was anything to do - the
   session may have gone down in the meantime, or it may be on that service type
   already - and the answer is shown the way the login self test shows one.
   It works on the configuration the program is running, not on what is on the
   page, since the session belongs to the running program. */
async function switchService(card, buttonNode) {
  const select = card.querySelector(".preferred-isp");
  const isp = select ? select.value : "";
  if (buttonNode) buttonNode.disabled = true;
  const res = await api("/api/action", {
    method: "POST",
    body: JSON.stringify({ action: "switchService", isp: isp }),
  });
  if (buttonNode) buttonNode.disabled = false;
  updateEntryActions();
  if (res.unreachable || !res.ok) {
    note("the sign-in as " + operatorName(isp) + " did not reach the program (" + res.status + ")");
    toast(t("msg.stale"), true);
    return;
  }
  const result = res.data.result || {};
  note("the sign-in as " + operatorName(isp) + " was asked for", {
    moved: !!res.data.moved,
    message: result.message,
  });
  if (!res.data.moved) {
    toast(t("acct.preferred.none"));
  } else if (result.success) {
    toast(t("acct.preferred.moved", operatorName(res.data.isp || "")));
  } else {
    // The portal's reply is localised by its ret_code, the way the login self
    // test localises it.
    const reason = state.i18n["retcode." + (result.retCode || "")] || result.message || "";
    toast(t("acct.preferred.failed", reason), true);
  }
  refreshStatus();
}

/* ---------- probes ---------- */

function renderProbes(list) {
  const box = $("probeList");
  box.innerHTML = "";
  list.forEach((value) => box.append(probeRow(value)));
}

function probeRow(value) {
  const input = el("input", {
    type: "text",
    spellcheck: "false",
    value: value || "",
    oninput: markDirty,
  });
  const remove = button(t("btn.remove"), "ghost");
  remove.onclick = () => { remove.parentElement.remove(); markDirty(); };
  return el("div", { class: "probe-row" }, input, remove);
}

function readProbes() {
  return Array.from($("probeList").querySelectorAll("input"))
    .map((input) => input.value.trim())
    .filter((value) => value !== "");
}

/* ---------- language ---------- */

async function switchLanguage(lang) {
  const res = await api("/api/bootstrap?lang=" + encodeURIComponent(lang));
  if (!res.ok) {
    note("the language was not changed: the bootstrap answered " + res.status);
    return;
  }
  state.i18n = res.data.i18n || state.i18n;
  state.operators = res.data.operators || state.operators;
  applyI18n();
  applyLang(res.data.lang);
  applyAutostart(res.data.autostart);
  const accounts = collect().accounts;
  const probes = readProbes();
  $("f-language").value = lang;
  renderProbes(probes);
  renderAccounts(accounts);
  renderStatus(res.data.status || state.status);
  fillWizardSelect(state.operators);
  markDirty();
  // Nothing is saved while the wizard is open: the account it is about is not in
  // the list yet, so the save would be refused with "at least one account" over
  // a step the reader has not reached, and that refusal was what the first run
  // showed when only the language had been picked. The wizard's own button saves
  // the language along with the account, and a reader who skips it changes the
  // language again on the page behind, where saving is what that means.
  if (!$("wizard").hidden) {
    note("the language was changed while the wizard is open, not saving it yet");
    return;
  }
  await submit();
}

/* ---------- wiring ---------- */

function switchTab(name) {
  document.querySelectorAll(".tab").forEach((tab) => {
    tab.classList.toggle("active", tab.dataset.tab === name);
  });
  document.querySelectorAll(".panel").forEach((panel) => {
    panel.classList.toggle("active", panel.id === "panel-" + name);
  });
  if (name === "about") loadLogTail();
}

async function loadLogTail() {
  const res = await api("/api/action", {
    method: "POST",
    body: JSON.stringify({ action: "logTail" }),
  });
  if (res.ok && res.data.lines) {
    $("logTail").textContent = res.data.lines.join("\n");
  }
}

function fillWizardSelect(operators) {
  const select = $("w-isp");
  select.innerHTML = "";
  operators.forEach((op) => {
    select.append(el("option", { value: op.Suffix, text: operatorName(op.Suffix) }));
  });
}

function wire() {
  document.querySelectorAll(".tab").forEach((tab) => {
    tab.addEventListener("click", () => switchTab(tab.dataset.tab));
  });

  ["f-interval", "f-timeout", "f-portal", "f-eportal", "f-prefix", "f-jsversion"]
    .forEach((id) => $(id).addEventListener("input", markDirty));
  ["f-remember", "f-autologin", "f-internet", "f-verbose", "f-preferred"]
    .forEach((id) => $(id).addEventListener("change", markDirty));

  $("f-language").addEventListener("change", () => switchLanguage($("f-language").value));

  $("f-autostart").addEventListener("change", async () => {
    const box = $("f-autostart");
    const res = await api("/api/action", {
      method: "POST",
      body: JSON.stringify({ action: "autostart", value: box.checked }),
    });
    if (!res.ok || !res.data.ok) {
      box.checked = !box.checked;
      toast(t("dlg.autostartFailed"), true);
      return;
    }
    applyAutostart(res.data.autostart);
    toast(t("msg.autostart"));
  });

  $("btnAddProbe").addEventListener("click", () => {
    $("probeList").append(probeRow(""));
    markDirty();
  });

  $("btnAddAccount").addEventListener("click", addAccount);
  $("btnSave").addEventListener("click", submit);

  $("btnCheck").addEventListener("click", async () => {
    await api("/api/action", { method: "POST", body: JSON.stringify({ action: "check" }) });
    toast(t("tray.checkRequested"));
    setTimeout(refreshStatus, 1200);
  });

  $("btnTestFirst").addEventListener("click", (event) => testEntry(0, event.currentTarget));

  $("btnReload").addEventListener("click", async () => {
    const res = await api("/api/action", { method: "POST", body: JSON.stringify({ action: "reload" }) });
    if (res.unreachable) {
      toast(t("msg.stale"), true);
      return;
    }
    if (res.ok && res.data.config) {
      state.errors = {};
      applyConfig(res.data.config);
      toast(t("msg.saved"));
      refreshStatus();
    }
  });

  $("btnOpenConfig").addEventListener("click", () => {
    api("/api/action", { method: "POST", body: JSON.stringify({ action: "openConfig" }) });
  });
  $("btnOpenLog").addEventListener("click", () => {
    api("/api/action", { method: "POST", body: JSON.stringify({ action: "openLog" }) });
  });

  $("btnQuit").addEventListener("click", async () => {
    if (!await confirmDialog(t("msg.confirmQuit"))) return;
    await api("/api/action", { method: "POST", body: JSON.stringify({ action: "quit" }) });
    document.body.replaceChildren(el("p", { class: "gone", text: t("msg.quitDone") }));
  });

  $("confirmYes").addEventListener("click", () => closeConfirm(true));
  $("confirmNo").addEventListener("click", () => closeConfirm(false));
  $("confirm").addEventListener("click", (event) => {
    if (event.target === $("confirm")) closeConfirm(false);
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") closeConfirm(false);
  });

  wireAccounts();

  $("w-language").addEventListener("change", () => switchLanguage($("w-language").value));
  $("btnWizardFinish").addEventListener("click", async () => {
    const username = $("w-username").value.trim();
    if (!username) {
      const slot = document.querySelector('[data-error="wizard"]');
      slot.textContent = t("err.accountRequired");
      note("the wizard was submitted with no account name");
      return;
    }
    const accounts = collect().accounts;
    accounts[0] = {
      username: username,
      password: $("w-password").value,
      isps: [$("w-isp").value],
      disabled: false,
    };
    // What is saved is the list the cards hold, so the account the wizard is
    // about has to be a card before anything is submitted. On a first run the
    // list is empty, and a request built from it carries no account at all,
    // which the server refuses as a missing account. The card keeps the password
    // it is given, see accountCard, so it is in the request as well.
    redraw(accounts);
    note("the wizard's account was put in the list as card 1, saving it");
    // submit hides the wizard once the server has taken the configuration, and
    // shows the refusal inside it when it has not.
    await submit();
  });

  $("btnWizardSkip").addEventListener("click", () => {
    $("wizard").hidden = true;
    note("the wizard was skipped; the settings page behind it is the way in");
  });

  window.addEventListener("beforeunload", (event) => {
    if (state.dirty) {
      event.preventDefault();
      event.returnValue = "";
    }
  });
}

/* The cards are rebuilt whenever the list changes, so their controls are wired
   once, on the list itself, and read the card they were clicked in. */
function wireAccounts() {
  const list = $("accountList");
  const indexOf = (node) => Number(node.closest(".account").dataset.index);

  list.addEventListener("click", (event) => {
    const target = event.target.closest("[data-act]");
    if (!target) return;
    const card = target.closest(".account");
    const index = indexOf(target);
    switch (target.dataset.act) {
      case "up": moveAccount(index, -1); break;
      case "down": moveAccount(index, 1); break;
      case "remove": removeAccount(index); break;
      case "test": testEntry(index, target); break;
      case "switchService": switchService(card, target); break;
      case "unservice":
        target.closest(".chip").remove();
        refreshPicker(card);
        markDirty();
        break;
      case "show": {
        const input = card.querySelector('[data-field="password"]');
        input.type = input.type === "password" ? "text" : "password";
        target.textContent = input.type === "password" ? t("field.show") : t("field.hide");
        break;
      }
    }
  });

  list.addEventListener("change", (event) => {
    const enabled = event.target.closest('[data-field="enabled"]');
    if (enabled) {
      // The badge marks an entry as out of the running, so it follows the switch
      // instead of waiting for a save.
      const off = enabled.closest(".account").querySelector(".badge.off");
      if (off) off.hidden = enabled.checked;
      return;
    }
    const picker = event.target.closest('[data-act="service-picker"]');
    if (!picker) return;
    const card = picker.closest(".account");
    const value = picker.value;
    if (value === "__add") return;
    if (value === "__custom") {
      const input = card.querySelector('[data-act="custom"]');
      input.hidden = false;
      input.focus();
      picker.value = "__add";
      return;
    }
    addService(card, value);
    refreshPicker(card);
  });

  // Drag and drop: only the grip starts a card drag, because a card that is
  // draggable everywhere would be dragged out of the middle of a password
  // selection. A chip is draggable itself, within its own row.
  let dragging = null;
  let draggingChip = null;

  list.addEventListener("mousedown", (event) => {
    const card = event.target.closest(".account");
    if (card) card.draggable = !!event.target.closest(".grip");
  });

  list.addEventListener("dragstart", (event) => {
    const chipNode = event.target.closest(".chip");
    if (chipNode) {
      draggingChip = chipNode;
      chipNode.classList.add("dragging");
      event.dataTransfer.effectAllowed = "move";
      event.dataTransfer.setData("text/plain", chipNode.dataset.suffix);
      return;
    }
    const card = event.target.closest(".account");
    if (!card || !card.draggable) return;
    dragging = card;
    card.classList.add("dragging");
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", card.dataset.index);
  });

  list.addEventListener("dragover", (event) => {
    if (draggingChip) {
      event.preventDefault();
      const over = event.target.closest(".chip");
      if (!over || over === draggingChip) return;
      const box = draggingChip.parentElement;
      // A service type belongs to one account, so a chip dragged over another
      // account's chip has nowhere to go: inserting it there is not something the
      // browser allows, and asking for it throws rather than doing nothing.
      if (over.parentElement !== box) return;
      const rect = over.getBoundingClientRect();
      const after = event.clientX > rect.left + rect.width / 2;
      box.insertBefore(draggingChip, after ? over.nextSibling : over);
      return;
    }
    if (!dragging) return;
    event.preventDefault();
    const over = event.target.closest(".account");
    if (!over || over === dragging) return;
    const rect = over.getBoundingClientRect();
    const after = event.clientY > rect.top + rect.height / 2;
    list.insertBefore(dragging, after ? over.nextSibling : over);
    renumber();
  });

  list.addEventListener("drop", (event) => event.preventDefault());

  list.addEventListener("dragend", () => {
    if (draggingChip) {
      draggingChip.classList.remove("dragging");
      draggingChip = null;
      markDirty();
      return;
    }
    if (!dragging) return;
    dragging.classList.remove("dragging");
    dragging.draggable = false;
    dragging = null;
    // The cards were moved one over the other as the pointer went across them, so
    // the list is already in the order the reader dropped it in; it is read back
    // out of the DOM the way every other edit reads it. Putting the cards back
    // into the position they came from would apply the move a second time, and a
    // dropped card would either jump home or land somewhere else entirely.
    state.status = Object.assign({}, state.status, { entryIndex: -1, skipped: [] });
    const dropped = collect().accounts;
    note("the list was dropped in this order", accountsBrief(dropped));
    redraw(dropped);
  });

  // The arrow keys move what has the focus: a chip within its account, a card
  // within the list. Both are orders the user sets and the program follows.
  list.addEventListener("keydown", (event) => {
    if (!event.altKey) return;

    const chipNode = event.target.closest(".chip");
    if (chipNode) {
      const sibling = event.key === "ArrowLeft" ? chipNode.previousElementSibling
        : event.key === "ArrowRight" ? chipNode.nextElementSibling
          : null;
      if (!sibling) return;
      event.preventDefault();
      const box = chipNode.parentElement;
      if (event.key === "ArrowLeft") box.insertBefore(chipNode, sibling);
      else box.insertBefore(sibling, chipNode);
      chipNode.focus();
      markDirty();
      return;
    }

    const card = event.target.closest(".account");
    if (!card) return;
    const index = Number(card.dataset.index);
    if (event.key === "ArrowUp") {
      event.preventDefault();
      moveAccount(index, -1);
    } else if (event.key === "ArrowDown") {
      event.preventDefault();
      moveAccount(index, 1);
    }
  });
}

async function boot() {
  const res = await api("/api/bootstrap");
  if (!res.ok) {
    note("the page cannot be used: the bootstrap was refused (" + res.status + "), the session is gone");
    document.body.replaceChildren(el("p", {
      class: "gone",
      text: bootFailedText(),
    }));
    return;
  }
  const data = res.data;
  state.i18n = data.i18n || {};
  state.operators = data.operators || [];
  state.configured = !!data.configured;

  applyI18n();
  applyLang(data.lang);
  applyAutostart(data.autostart);
  fillWizardSelect(state.operators);
  applyConfig(data.config || {});
  renderStatus(data.status || {});

  $("version").textContent = data.version;
  $("platform").textContent = data.platform;
  $("abVersion").textContent = data.version;
  $("abPlatform").textContent = data.platform;
  $("abRepo").textContent = data.repo;
  $("abRepo").href = data.repo;
  $("abConfigPath").textContent = (data.paths || {}).config || "-";
  $("abLogPath").textContent = (data.paths || {}).log || "-";
  $("w-language").value = data.config ? (data.config.language || "auto") : "auto";

  wire();
  if (!state.configured) $("wizard").hidden = false;
  note("the page is ready", {
    version: data.version,
    configured: state.configured,
    accounts: (data.config && data.config.accounts ? data.config.accounts.length : 0),
    wizard: !state.configured,
    language: data.config ? data.config.language : "",
  });

  setInterval(refreshStatus, 5000);
}

boot();
