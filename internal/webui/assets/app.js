"use strict";

/* The address the page was opened with carried a token, which the server
   exchanged for this cookie before serving the page, so the address bar carries
   nothing. The page repeats the cookie in a header of its own; a request that
   changes something is accepted only with both. */
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
   to the browser's console under this prefix, so a report of "the button did
   nothing" can be read back in the order it happened. */
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
   bootstrap failed has to take the language from the browser. Both wordings
   live here: there is no table to read, and nowhere to type a token. They are
   the msg.stale wording, and the assets test holds them to it. */
function bootFailedText() {
  if ((navigator.language || "").toLowerCase().startsWith("zh")) {
    return "设置页已失效，请从托盘重新打开。";
  }
  return "This page is no longer valid, open it again from the tray.";
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
  $("f-passwordfile").value = cfg.passwordFile || "";
  $("f-autologin").checked = !!cfg.autoLogin;
  $("f-internet").checked = !!cfg.checkInternet;
  $("f-verbose").checked = !!cfg.verboseLog;
  $("f-preferred").checked = !!cfg.switchToPreferredService;
  $("f-updates").checked = !!cfg.checkForUpdates;
  $("f-channel").value = cfg.updateChannel === "prerelease" ? "prerelease" : "stable";
  $("f-language").value = cfg.language || "auto";
  renderProbes(cfg.probeUrls || []);
  renderAccounts(cfg.accounts || []);
}

function renderAccounts(accounts, focusFirst) {
  const list = $("accountList");
  list.innerHTML = "";
  accounts.forEach((entry, index) => list.append(accountCard(entry, index)));
  $("acctEmpty").hidden = accounts.length > 0;
  $("acctWarn").hidden = !(accounts.length > 0 && accounts.every((entry) => entry.disabled));
  applyErrors(focusFirst);
  applyStatusBadges(state.status);
  updateEntryActions();
}

// The test button and the switch ask the running program, whose list is keyed by
// position, so an entry just moved or edited is not what it uses yet: both wait for a save.
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

  // Where the server said this entry's password comes from. "file" means it is
  // read from the file the configuration names, so the field below is closed,
  // and the note under it names that file.
  const source = entry.passwordSource || "";
  const fromFile = source === "file";
  const passwordFile = (state.config && state.config.passwordFile) || "";

  const number = el("span", { class: "account-no", text: String(index + 1) });
  const grip = el("span", { class: "grip", title: t("acct.drag"), "aria-hidden": "true", text: "⠿" });
  const card = el("article", {
    class: "account",
    dataset: { index: String(index), source: source },
  });

  const head = el("div", { class: "account-head" },
    grip,
    number,
    el("span", {
      class: "account-title",
      text: entry.username || t("acct.card", index + 1),
    }),
    // The three states an entry can be in besides usable: the session is on it,
    // the portal refused it for a while, or it is switched off.
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

  // The page is never given the stored password: an empty field means "keep the
  // stored one", and what the reader typed is put back as the card is rebuilt.
  // One the server reads from a file is not the page's to write, so that field
  // is closed instead, and the note below it names the file.
  const password = el("input", {
    id: passwordID,
    type: "password",
    autocomplete: "off",
    value: entry.password || "",
    placeholder: fromFile ? null
      : source ? t("field.password.keep") : t("field.password"),
    disabled: fromFile,
    oninput: markDirty,
    dataset: { field: "password" },
  });

  const show = button(t("field.show"), "ghost");
  show.dataset.act = "show";
  // Nothing to reveal in a field that is closed and holds nothing.
  show.disabled = fromFile;

  // What the card says instead of the field when a password comes from a file.
  // A file named for this run alone leaves no path for the note to name.
  const passwordNote = !fromFile ? null
    : passwordFile ? t("field.password.fromFile", passwordFile)
      : t("field.password.fromFile.unnamed");

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

  // Shown only on the card the session is on, and only while its entry lists
  // another service type to move it to; applyStatusBadges decides both from the
  // status it is handed.
  const switchButton = button(t("acct.preferred.btn"), "ghost");
  switchButton.dataset.act = "switchService";
  // The one way out the program cannot take itself: the portal's own page, for
  // signing the session out while the portal refuses to sign in again. It belongs
  // to the refusal, so it is created hidden and renderPreferred uncovers it there.
  const portalButton = button(t("acct.preferred.portal"), "ghost");
  portalButton.dataset.act = "openPortal";
  portalButton.hidden = true;
  const preferred = el("div", { class: "account-preferred", hidden: true },
    el("p", { class: "hint preferred-line" }),
    el("div", { class: "preferred-actions" },
      el("span", { class: "preferred-target", text: t("acct.preferred.choose") }),
      el("select", { class: "preferred-isp" }),
      switchButton,
      portalButton));

  const body = el("div", { class: "account-body" },
    row(usernameID, t("field.account"), username, t("field.account.hint"), "accounts." + index + ".username"),
    row(passwordID, t("field.password"), el("div", { class: "input-row" }, password, show),
      passwordNote, "accounts." + index + ".password"),
    // The service types are answered for by position, one chip at a time, so the
    // row carries one error line for the whole list.
    row(null, t("acct.services"), el("div", { class: "services" },
      chips,
      el("div", { class: "service-add" }, picker, custom),
      el("p", { class: "hint", text: t("acct.services.hint") })), null,
      "accounts." + index + ".isps"),
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

// A chip is one service type of one account, dragged or moved with the arrow keys
// while focused, because their order is the order the portal is asked in.
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
    // Where the server said this entry's password comes from. It is carried
    // through a rebuild, so a card keeps saying it across a move or a removal.
    passwordSource: card.dataset.source || "",
    disabled: !card.querySelector('[data-field="enabled"]').checked,
    isps: Array.from(card.querySelectorAll(".chip")).map((node) => node.dataset.suffix),
  }));
  return {
    accounts,
    rememberPassword: $("f-remember").checked,
    passwordFile: $("f-passwordfile").value.trim(),
    portalBaseUrl: $("f-portal").value.trim(),
    eportalUrl: $("f-eportal").value.trim(),
    accountPrefix: $("f-prefix").value,
    checkIntervalSeconds: parseInt($("f-interval").value, 10) || 0,
    httpTimeoutSeconds: parseInt($("f-timeout").value, 10) || 0,
    autoLogin: $("f-autologin").checked,
    switchToPreferredService: $("f-preferred").checked,
    checkForUpdates: $("f-updates").checked,
    updateChannel: $("f-channel").value,
    checkInternet: $("f-internet").checked,
    verboseLog: $("f-verbose").checked,
    probeUrls: readProbes(),
    jsVersion: $("f-jsversion").value.trim(),
    language: $("f-language").value,
  };
}

/* requestBody is what a save sends: the cards as collect reads them, without
   the source of a password. That source is the server's word about an entry,
   read to draw the card and not something the page has to say back. */
function requestBody() {
  const body = collect();
  body.accounts.forEach((entry) => { delete entry.passwordSource; });
  return body;
}

/* accountsBrief is the list as a console line: the name of each entry and
   whether it carries a password, so a refused save can be read back without the
   passwords ever being written down. */
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

// activeEntryLabel names the entry the session is on, with the single service
// type it is on. The program reads that off the account the portal echoes: a
// suffix the echo does not carry is one the login did not send.
// checkedAgo says how long ago the round that produced the rest of the status
// read the portal. An unparseable or absent stamp reads as "-", never as "just
// now", which would be a claim the page cannot make.
function checkedAgo(raw) {
  if (!raw) return "-";
  const then = Date.parse(raw);
  if (isNaN(then)) return "-";
  const seconds = Math.max(0, Math.round((Date.now() - then) / 1000));
  if (seconds < 60) return t("status.checkedAgo", seconds);
  return t("status.checkedMin", Math.round(seconds / 60));
}

function activeEntryLabel(st) {
  if (!st || typeof st.entryIndex !== "number" || st.entryIndex < 0) return "-";
  const cfg = state.config;
  if (!cfg || !cfg.accounts || st.entryIndex >= cfg.accounts.length) return "-";
  // The service type is known only while a session has been read, in the online
  // state: otherwise the entry is named with the types it may be used with, as
  // for a status that carries no service type.
  if (st.state === "online" && typeof st.usedIsp === "string") {
    return entryLabel(st.entryIndex, st.usedIsp);
  }
  return entryLabel(st.entryIndex);
}

function formatWait(seconds) {
  if (seconds < 60) return seconds + "s";
  return Math.floor(seconds / 60) + ":" + String(seconds % 60).padStart(2, "0");
}

// renderUpdate is the line the last look for a newer release left. The wording
// comes from the program and not from the table here: it is the same line
// the log and the update command carry, and it names versions and a file path.
// What is left to this page is the case where no look has happened.
function renderUpdate(up) {
  const field = $("abUpdate");
  if (!field) return;
  if (!up) {
    field.textContent = t("about.update.idle");
    field.classList.remove("bad");
    return;
  }
  field.textContent = up.installed ? up.message + " (" + up.installed + ")" : up.message;
  // A newer release this copy cannot take is worth marking, since the reader has
  // to do it themselves.
  field.classList.toggle("bad", !!up.newer && (!up.signed || !up.installable));
}

function wireUpdateButton() {
  const btn = $("btnCheckUpdate");
  if (!btn) return;
  btn.addEventListener("click", async () => {
    btn.disabled = true;
    const field = $("abUpdate");
    if (field) field.textContent = t("state.checking");
    try {
      const res = await api("/api/action", {
        method: "POST",
        body: JSON.stringify({ action: "checkUpdate" }),
      });
      if (res.unreachable || !res.ok) {
        toast(t((res.data && res.data.error) || "err.badRequest"), true);
        return;
      }
      // The look runs on its own, so what it found arrives with a later poll and
      // not with this reply. The row above holds the place until then.
    } finally {
      btn.disabled = false;
    }
  });
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
    // entry says it should be, and the way to move it there. The line also says
    // whether the program's own attempt was refused, visible nowhere else.
    renderPreferred(card, st, index);
  });
  updateSummary();
}

// renderPreferred fills in the move-the-session block of one card.
function renderPreferred(card, st, index) {
  const block = card.querySelector(".account-preferred");
  if (!block) return;
  // Only the card of the entry the session is on, and only while that session is
  // up: the block is about a session that exists, not about other cards.
  const targets = st.state === "online" && st.entryIndex === index ? switchTargets(st, index) : [];
  // Written only when the answer changes: this runs for every card on every
  // refresh, and a line per poll would bury everything else in the console.
  const shows = targets.length > 0;
  if (block.hidden === shows) {
    note(shows
      ? "card " + (index + 1) + " offers moving the session onto another service type"
      : "no card offers moving the session", {
      state: st.state,
      entryIndex: st.entryIndex,
      usedIsp: st.usedIsp,
      preferredIsp: st.preferredIsp,
      targets,
      refused: !!st.switchRefused,
    });
  }
  block.hidden = !shows;
  if (!shows) return;

  const select = block.querySelector(".preferred-isp");
  // What the reader picked is kept while it is still one of the ways out, because
  // the list is rebuilt on every status refresh. An empty value is the campus
  // network itself, so the first render is told apart by the select having no options.
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
  // The portal page is offered with the refusal and nowhere else: without one
  // there is nothing on the portal to go and do.
  const portalButton = block.querySelector('[data-act="openPortal"]');
  if (portalButton) portalButton.hidden = !refused;
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
  // A session that is up and that the program could not move where the login
  // list says it should be needs a reader: the state reads as "nothing to do".
  const refused = st.switchRefused;
  $("statePill").classList.toggle("attention", !!refused);
  // The pill carries the state and, once something is signed in, who it is signed
  // in as: the account and the service type the session is on. That is the reading
  // looked at most, and the strip of readings is a row lower than it needs to be.
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
  // The page polls faster than the loop runs and a round takes as long as the
  // portal does, so the age of this reading is shown and not left implied:
  // a state measured four minutes ago otherwise reads as the state right now.
  $("stChecked").textContent = checkedAgo(st.checkedAt);
  // The notice line carries what the readings cannot: the program's own reason
  // for the state, and a refusal that needs a reader. The refusal comes first,
  // since it names the buttons that are the way out.
  const notice = $("statusNotice");
  const reason = st.message || st.lastError || "";
  if (refused) {
    notice.textContent = t("status.switchRefused",
      operatorName(st.preferredIsp || ""), operatorName(st.usedIsp || ""));
    notice.classList.remove("bad");
  } else if (reason) {
    notice.textContent = reason;
    notice.classList.toggle("bad", code === "error");
  } else {
    notice.textContent = "";
  }
  notice.hidden = !notice.textContent;
  renderUpdate(st.update);
  applyStatusBadges(state.status);
}

/* ---------- errors and toasts ---------- */

/* The config fields whose input id is not the field path the server answers with.
   The rest of the paths address the login list or a probe by position, and are
   read back below. */
const FIELD_INPUTS = {
  checkIntervalSeconds: "f-interval",
  httpTimeoutSeconds: "f-timeout",
  portalBaseUrl: "f-portal",
  eportalUrl: "f-eportal",
};

/* nearestSlot is the message slot for a field path, or the slot of the thing the
   path belongs to: the server names one probe of a list the page keeps one line
   for, and an entry of a list the page holds one error line for. */
function nearestSlot(field) {
  let path = field;
  for (;;) {
    const slot = document.querySelector('[data-error="' + path + '"]');
    if (slot) return slot;
    const cut = path.lastIndexOf(".");
    if (cut < 0) return null;
    path = path.slice(0, cut);
  }
}

/* fieldTarget is where one field path of a refusal is reported, which control it
   is marked on, and what the focus goes to. Every path config.Validate returns
   has all three, so no refusal is written where nothing can be seen. */
function fieldTarget(field) {
  const entry = field.match(/^accounts\.(\d+)\.(username|password)$/);
  if (entry) {
    const card = document.querySelector('.account[data-index="' + entry[1] + '"]');
    if (card) {
      const input = card.querySelector('[data-field="' + entry[2] + '"]');
      return {
        slot: card.querySelector('[data-error="' + field + '"]') || nearestSlot(field),
        control: input,
        focus: input,
      };
    }
  }

  const services = field.match(/^accounts\.(\d+)\.isps(?:\.(\d+))?$/);
  if (services) {
    const card = document.querySelector('.account[data-index="' + services[1] + '"]');
    if (card) {
      const row = card.querySelector("[data-chips]");
      const chips = Array.from(row.children);
      return {
        slot: card.querySelector('[data-error="accounts.' + services[1] + '.isps"]') ||
          nearestSlot(field),
        // A list of service types has no input to mark: the row of chips carries
        // the mark, and the focus goes to the chip the position names, or to the
        // first of them.
        control: row,
        focus: chips[Number(services[2] || 0)] || chips[0] || row,
      };
    }
  }

  const probe = field.match(/^probeUrls\.(\d+)$/);
  if (probe) {
    // readProbes drops the blank rows before the request is sent, so the position
    // the server reports is the position among the rows that hold an address.
    const filled = Array.from($("probeList").querySelectorAll("input"))
      .filter((input) => input.value.trim() !== "");
    const input = filled[Number(probe[1])] || filled[0] || $("btnAddProbe");
    return { slot: nearestSlot(field), control: input, focus: input };
  }

  const id = FIELD_INPUTS[field];
  const input = id ? $(id) : null;
  return { slot: nearestSlot(field), control: input, focus: input };
}

/* applyErrors puts a refused save next to the fields it is about. A field the
   showing tab does not carry is reached by switching to its tab, and a message
   with nowhere to go is toasted, so a refusal is never silent. focusFirst is set
   by the save alone, the one caller that may move the reader to the field. */
function applyErrors(focusFirst) {
  document.querySelectorAll("[data-error]").forEach((node) => { node.textContent = ""; });
  document.querySelectorAll(".invalid").forEach((node) => node.classList.remove("invalid"));

  let shown = 0;
  let firstTarget = null;
  Object.entries(state.errors).forEach(([field, key]) => {
    // The whole-request failure has no field: it is toasted below.
    if (field === "_") return;
    const target = fieldTarget(field);
    if (target.slot) {
      target.slot.textContent = t(key);
      shown++;
    }
    if (target.control) target.control.classList.add("invalid");
    if (!firstTarget && (target.slot || target.control)) firstTarget = target;
  });

  // The wizard lies over the whole page, so a message belonging to a field
  // underneath it cannot be seen: while it is open it carries the first one
  // itself, or a refused save would look like nothing happened.
  if (!$("wizard").hidden) {
    const key = Object.values(state.errors)[0];
    const slot = document.querySelector('[data-error="wizard"]');
    if (key && slot) {
      slot.textContent = t(key);
      shown++;
    }
  } else if (firstTarget && focusFirst) {
    // The panel the message is in may not be the one showing, and a message
    // written into a hidden panel reads as no answer.
    const panel = (firstTarget.slot || firstTarget.control).closest(".panel");
    if (panel && !panel.classList.contains("active")) {
      switchTab(panel.id.replace(/^panel-/, ""));
    }
    // The control that message is about takes the focus, and it is in the panel
    // just switched to.
    if (firstTarget.focus) firstTarget.focus.focus();
  }

  const generic = state.errors["_"];
  if (generic) {
    toast(t(generic), true);
  } else if (shown === 0) {
    // Nothing on the page could carry the message.
    const key = Object.values(state.errors)[0];
    if (key) toast(t(key), true);
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

/* The wizard lies over the whole page, so while it is up the page behind it is
   inert: Tab cannot reach a control the reader cannot see. The modals are left
   out of it, since one of them is the dialog in use. */
function setPageInert(on) {
  Array.from(document.body.children).forEach((node) => {
    if (node.classList.contains("modal")) return;
    node.inert = on;
  });
}

function openWizard() {
  $("wizard").hidden = false;
  setPageInert(true);
  // The keyboard starts inside the dialog, on its first control.
  $("w-language").focus();
}

function closeWizard() {
  if ($("wizard").hidden) return;
  $("wizard").hidden = true;
  setPageInert(false);
}

/* ---------- saving ---------- */

async function submit() {
  const btn = $("btnSave");
  btn.disabled = true;
  btn.textContent = t("msg.saving");
  const body = requestBody();
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
    renderAccounts(collect().accounts, true);
    return false;
  }
  note("the configuration was saved");
  state.errors = {};
  state.dirty = false;
  applyConfig(res.data.config);
  $("dirtyBadge").hidden = true;
  toast(t("msg.saved"));
  state.configured = true;
  closeWizard();
  refreshStatus();
  return true;
}

let warnedStale = false;

async function refreshStatus() {
  const res = await api("/api/status");
  if (res.unreachable || !res.ok) {
    // Both mean this page's session is gone: the program quit, or the settings
    // service shut down while the tab was idle. The way back is to open the page
    // again from the tray.
    if (!warnedStale) {
      warnedStale = true;
      // Said once per page, in the console as well as on screen: the line says
      // which of the two it was.
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
   type picked on the card, which replaces the session the portal holds: the
   portal allows one session per client address. */
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

/* The way out of a session the portal will not sign in again while it is up: the
   session has to be signed out on the portal's own page, which the program cannot
   do. The address is the one the program is configured with, so it carries nothing. */
async function openPortal() {
  note("asking the program to open the portal page");
  const res = await api("/api/action", { method: "POST", body: JSON.stringify({ action: "openPortal" }) });
  if (res.unreachable) {
    note("the request to open the portal page did not reach the program");
    toast(t("msg.stale"), true);
    return;
  }
  if (!res.ok || !res.data.ok) {
    note("the portal page could not be opened", { status: res.status, error: res.data.error });
    toast(t(res.data.error || "err.openPortal"), true);
    return;
  }
  note("the portal page was handed to the browser", res.data.url);
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
  // the list yet, so the save would be refused. The wizard's own button saves the
  // language with the account, and one who skips it changes it on the page behind.
  if (!$("wizard").hidden) {
    note("the language was changed while the wizard is open, not saving it yet");
    return;
  }
  await submit();
}

/* ---------- wiring ---------- */

function switchTab(name) {
  // The strip is a tablist: the showing tab carries the selection, and it is the
  // one Tab reaches, since the others are stepped through with the arrow keys.
  document.querySelectorAll(".tab").forEach((tab) => {
    const on = tab.dataset.tab === name;
    tab.classList.toggle("active", on);
    tab.setAttribute("aria-selected", on ? "true" : "false");
    tab.tabIndex = on ? 0 : -1;
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
  const tabs = Array.from(document.querySelectorAll(".tab"));
  tabs.forEach((tab, index) => {
    tab.addEventListener("click", () => switchTab(tab.dataset.tab));
    // Left and right step through the strip and show what they land on; the ends
    // wrap, so the strip is one loop.
    tab.addEventListener("keydown", (event) => {
      const step = event.key === "ArrowRight" ? 1
        : event.key === "ArrowLeft" ? -1 : 0;
      if (!step) return;
      event.preventDefault();
      const next = tabs[(index + step + tabs.length) % tabs.length];
      switchTab(next.dataset.tab);
      next.focus();
    });
  });

  ["f-interval", "f-timeout", "f-portal", "f-eportal", "f-prefix",
    "f-jsversion", "f-passwordfile"]
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
    // The reload throws away whatever is in the form, so a reader with unsaved
    // edits is asked first, and told afterwards what became of them.
    if (state.dirty && !await confirmDialog(t("msg.confirmReload"))) return;
    const res = await api("/api/action", { method: "POST", body: JSON.stringify({ action: "reload" }) });
    if (res.unreachable) {
      toast(t("msg.stale"), true);
      return;
    }
    if (res.ok && res.data.config) {
      state.errors = {};
      state.dirty = false;
      applyConfig(res.data.config);
      $("dirtyBadge").hidden = true;
      toast(t("msg.reloaded"));
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
    if (event.key !== "Escape") return;
    // The confirm box lies over the wizard, which lies over the page: Escape
    // closes the one on top.
    if (!$("confirm").hidden) {
      closeConfirm(false);
      return;
    }
    if (!$("wizard").hidden) {
      closeWizard();
      note("the wizard was closed with Escape; the settings page behind it is the way in");
    }
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
    // What is saved is the list the cards hold, so the account the wizard is about
    // has to be a card first: on a first run the list is empty, and a request built
    // from it would carry no account. The card keeps the password it is given.
    redraw(accounts);
    note("the wizard's account was put in the list as card 1, saving it");
    // submit hides the wizard once the server has taken the configuration, and
    // shows the refusal inside it when it has not.
    await submit();
  });

  $("btnWizardSkip").addEventListener("click", () => {
    closeWizard();
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
      case "openPortal": openPortal(); break;
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

  // Only the grip starts a card drag, because a card draggable everywhere would be
  // dragged out of the middle of a password selection. A chip is draggable itself.
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
      // account's chip has nowhere to go: asking for it would throw.
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
    // The cards moved as the pointer crossed them, so the DOM has the order.
    state.status = Object.assign({}, state.status, { entryIndex: -1, skipped: [] });
    const dropped = collect().accounts;
    note("the list was dropped in this order", accountsBrief(dropped));
    redraw(dropped);
  });

  // The arrow keys move what has the focus: a chip within its account, a card
  // within the list.
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
  wireUpdateButton();
  if (!state.configured) openWizard();
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
