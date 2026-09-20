"use strict";

const token = new URLSearchParams(location.search).get("token") || "";

const state = {
  i18n: {},
  config: null,
  status: null,
  dirty: false,
  configured: true,
};

const $ = (id) => document.getElementById(id);

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
  const sep = path.includes("?") ? "&" : "?";
  const resp = await fetch(path + sep + "token=" + encodeURIComponent(token), opts);
  const data = await resp.json().catch(() => ({}));
  return { ok: resp.ok, status: resp.status, data };
}

/* ---------- rendering ---------- */

function applyI18n() {
  document.querySelectorAll("[data-i18n]").forEach((el) => {
    el.textContent = t(el.dataset.i18n);
  });
  document.querySelectorAll("[data-i18n-ph]").forEach((el) => {
    el.placeholder = t(el.dataset.i18nPh);
  });
  document.title = t("app.name");
}

function applyLang(code) {
  document.documentElement.lang = code === "zh" ? "zh-CN" : "en";
}

function applyConfig(cfg) {
  state.config = cfg;
  $("f-username").value = cfg.username || "";
  $("f-password").value = cfg.password || "";
  $("f-interval").value = cfg.checkIntervalSeconds;
  $("f-timeout").value = cfg.httpTimeoutSeconds;
  $("f-portal").value = cfg.portalBaseUrl || "";
  $("f-eportal").value = cfg.eportalUrl || "";
  $("f-prefix").value = cfg.accountPrefix || "";
  $("f-suffix").value = cfg.accountSuffix || "";
  $("f-jsversion").value = cfg.jsVersion || "";
  $("f-remember").checked = !!cfg.rememberPassword;
  $("f-autologin").checked = !!cfg.autoLogin;
  $("f-internet").checked = !!cfg.checkInternet;
  $("f-verbose").checked = !!cfg.verboseLog;
  $("f-language").value = cfg.language || "auto";
  $("f-isp").value = cfg.accountSuffix || "";
  renderProbes(cfg.probeUrls || []);
}

function renderProbes(list) {
  const box = $("probeList");
  box.innerHTML = "";
  list.forEach((value) => box.appendChild(probeRow(value)));
}

function probeRow(value) {
  const row = document.createElement("div");
  row.className = "probe-row";
  const input = document.createElement("input");
  input.type = "text";
  input.spellcheck = false;
  input.value = value || "";
  const remove = document.createElement("button");
  remove.type = "button";
  remove.className = "btn ghost";
  remove.textContent = t("btn.remove");
  remove.addEventListener("click", () => {
    row.remove();
    markDirty();
  });
  input.addEventListener("input", markDirty);
  row.append(input, remove);
  return row;
}

function readProbes() {
  return Array.from($("probeList").querySelectorAll("input"))
    .map((i) => i.value.trim())
    .filter((v) => v !== "");
}

function renderStatus(st) {
  state.status = st;
  const pill = $("statePill");
  pill.className = "state-pill " + (st.state || "checking");
  $("stateText").textContent = t("state." + (st.state || "checking"));
  $("stClientIp").textContent = st.clientIp || "-";
  $("stAccount").textContent = st.account || "-";
  $("stSince").textContent = st.onlineSince || "-";
  $("stLastLogin").textContent = st.lastLoginAt || "-";
  $("stInternet").textContent = st.internetChecked
    ? (st.internetOk ? t("status.internetOk") : t("status.internetNo"))
    : "-";
}

function renderErrors(errors) {
  document.querySelectorAll("[data-error]").forEach((el) => { el.textContent = ""; el.dataset.key = ""; });
  document.querySelectorAll(".invalid").forEach((el) => el.classList.remove("invalid"));
  if (!errors) return;
  Object.keys(errors).forEach((field) => {
    const key = errors[field];
    const slot = document.querySelector('[data-error="' + field + '"]');
    if (slot) {
      slot.textContent = t(key);
      slot.dataset.key = key;
    }
    const input = $("f-" + field) || document.querySelector("[name=" + field + "]");
    if (input) input.classList.add("invalid");
  });
  const generic = errors["_"];
  if (generic && !Object.keys(errors).some((k) => k !== "_")) {
    toast(t(generic), true);
  }
}

let toastTimer = null;
function toast(text, isError) {
  const el = $("toast");
  el.textContent = text;
  el.style.color = isError ? "var(--err)" : "var(--ok)";
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, 6000);
}

function markDirty() {
  state.dirty = true;
  $("dirtyBadge").hidden = false;
}

/* ---------- collecting and saving ---------- */

function collect() {
  return {
    username: $("f-username").value.trim(),
    password: $("f-password").value,
    rememberPassword: $("f-remember").checked,
    portalBaseUrl: $("f-portal").value.trim(),
    eportalUrl: $("f-eportal").value.trim(),
    accountPrefix: $("f-prefix").value,
    accountSuffix: $("f-isp").value,
    checkIntervalSeconds: parseInt($("f-interval").value, 10) || 0,
    httpTimeoutSeconds: parseInt($("f-timeout").value, 10) || 0,
    autoLogin: $("f-autologin").checked,
    checkInternet: $("f-internet").checked,
    verboseLog: $("f-verbose").checked,
    probeUrls: readProbes(),
    jsVersion: $("f-jsversion").value.trim(),
    language: $("f-language").value,
  };
}

async function submit() {
  const btn = $("btnSave");
  btn.disabled = true;
  const res = await api("/api/config", { method: "POST", body: JSON.stringify(collect()) });
  btn.disabled = false;
  if (!res.ok || !res.data.ok) {
    renderErrors(res.data.errors || { _: "err.saveFailed" });
    return false;
  }
  renderErrors(null);
  applyConfig(res.data.config);
  state.dirty = false;
  $("dirtyBadge").hidden = true;
  toast(t("msg.saved"));
  state.configured = true;
  $("wizard").hidden = true;
  refreshStatus();
  return true;
}

async function refreshStatus() {
  const res = await api("/api/status");
  if (res.ok && res.data.status) renderStatus(res.data.status);
}

/* ---------- language ---------- */

async function switchLanguage(lang) {
  const res = await api("/api/bootstrap?lang=" + encodeURIComponent(lang));
  if (!res.ok) return;
  state.i18n = res.data.i18n || state.i18n;
  applyI18n();
  renderProbes(readProbes());
  renderStatus(res.data.status || state.status);
  $("f-language").value = lang;
  $("f-isp").value = state.config ? state.config.accountSuffix : "";
  markDirty();
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

function renderTest(test) {
  const box = $("testResult");
  const retCode = test.result.retCode || "";
  const message = state.i18n["retcode." + retCode] || test.result.message || "";
  const lines = [
    t("status.state") + ": " + t("state." + (test.after.state || "checking")),
    "ret_code: " + (retCode === "" ? "-" : retCode) + "  " + message,
    "raw: " + (test.result.raw || "").trim(),
  ];
  box.textContent = lines.join("\n");
  box.hidden = false;
  toast(test.result.success ? t("msg.testAccepted") : t("msg.testRejected", message), !test.result.success);
}

function wire() {
  document.querySelectorAll(".tab").forEach((tab) => {
    tab.addEventListener("click", () => switchTab(tab.dataset.tab));
  });

  ["f-username", "f-password", "f-interval", "f-timeout", "f-portal", "f-eportal",
    "f-prefix", "f-suffix", "f-jsversion"].forEach((id) => {
      $(id).addEventListener("input", markDirty);
    });
  ["f-remember", "f-autologin", "f-internet", "f-verbose"].forEach((id) => {
    $(id).addEventListener("change", markDirty);
  });
  $("f-isp").addEventListener("change", () => {
    $("f-suffix").value = $("f-isp").value;
    markDirty();
  });
  $("f-language").addEventListener("change", () => switchLanguage($("f-language").value));

  $("btnShow").addEventListener("click", () => {
    const box = $("f-password");
    box.type = box.type === "password" ? "text" : "password";
  });

  $("btnAddProbe").addEventListener("click", () => {
    $("probeList").appendChild(probeRow(""));
    markDirty();
  });

  $("btnSave").addEventListener("click", submit);

  $("btnCheck").addEventListener("click", async () => {
    await api("/api/action", { method: "POST", body: JSON.stringify({ action: "check" }) });
    toast(t("tray.checkRequested"));
    setTimeout(refreshStatus, 1200);
  });

  $("btnTest").addEventListener("click", async () => {
    toast(t("msg.testRunning"));
    const res = await api("/api/action", { method: "POST", body: JSON.stringify({ action: "testLogin" }) });
    if (res.ok) renderTest(res.data);
  });

  $("btnReload").addEventListener("click", async () => {
    const res = await api("/api/action", { method: "POST", body: JSON.stringify({ action: "reload" }) });
    if (res.ok && res.data.config) {
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
    if (!confirm(t("msg.confirmQuit"))) return;
    await api("/api/action", { method: "POST", body: JSON.stringify({ action: "quit" }) });
    document.body.innerHTML = "<p style='padding:40px;font-family:sans-serif'>" + t("msg.quitDone") + "</p>";
  });

  $("w-language").addEventListener("change", () => switchLanguage($("w-language").value));
  $("btnWizardFinish").addEventListener("click", async () => {
    const user = $("w-username").value.trim();
    if (!user) {
      const slot = document.querySelector('[data-error="wizard"]');
      slot.textContent = t("err.accountRequired");
      return;
    }
    $("f-username").value = user;
    $("f-password").value = $("w-password").value;
    $("f-isp").value = $("w-isp").value;
    $("f-suffix").value = $("w-isp").value;
    if (await submit()) {
      $("wizard").hidden = true;
    }
  });

  window.addEventListener("beforeunload", (e) => {
    if (state.dirty) {
      e.preventDefault();
      e.returnValue = "";
    }
  });
}

function fillSelects(operators) {
  const fill = (select, value) => {
    select.innerHTML = "";
    operators.forEach((op) => {
      const opt = document.createElement("option");
      opt.value = op.Suffix;
      opt.textContent = op.Name;
      select.appendChild(opt);
    });
    select.value = value || "";
  };
  fill($("f-isp"), "");
  fill($("w-isp"), "");
}

async function boot() {
  const res = await api("/api/bootstrap");
  if (!res.ok) {
    document.body.innerHTML =
      "<p style='padding:40px;font-family:sans-serif'>This page needs the one time token from the tray menu.</p>";
    return;
  }
  const data = res.data;
  state.i18n = data.i18n || {};
  state.configured = !!data.configured;

  applyI18n();
  applyLang(data.lang);
  fillSelects(data.operators || []);
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
  $("w-isp").value = data.config ? (data.config.accountSuffix || "") : "";

  wire();
  if (!state.configured) $("wizard").hidden = false;

  setInterval(refreshStatus, 5000);
}

boot();
