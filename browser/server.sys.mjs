import { State, logError as log, paths } from "resource://slybrowser/state.sys.mjs";
import {
  browserWindow,
  browserWindows,
  describeFolder,
  describeTab,
  folderTabs,
  getNamedTabs,
  getSpaceContext,
  getTab,
  postLazyTab,
  putSpaceLook,
  putTabContext,
  tabLogin,
  realTabs,
  tabId,
  tabUrl,
} from "resource://slybrowser/tabs.sys.mjs";
import { attachWindows, postGroupTab, refreshWindows } from "resource://slybrowser/window.sys.mjs";

const lazy = {};
ChromeUtils.defineESModuleGetters(lazy, {
  PlacesUtils: "resource://gre/modules/PlacesUtils.sys.mjs",
  ContextualIdentityService: "moz-src:///toolkit/components/contextualidentity/ContextualIdentityService.sys.mjs",
  gZenSpaceRoutingManager: "resource:///modules/zen/spacerouting/ZenSpaceRoutingManager.sys.mjs",
});

const version = "2";
let server = null;

export function start() {
  if (server) {
    return;
  }
  const file = Cc["@mozilla.org/file/local;1"].createInstance(Ci.nsIFile);
  file.initWithPath(paths.socket);
  if (!file.parent.exists()) {
    file.parent.create(Ci.nsIFile.DIRECTORY_TYPE, 0o700);
  }
  if (file.exists()) {
    file.remove(false);
  }
  server = Cc["@mozilla.org/network/server-socket;1"].createInstance(Ci.nsIServerSocket);
  server.initWithFilename(file, 0o600, -1);
  server.asyncListen({
    onSocketAccepted(_server, transport) {
      accept(transport);
    },
    onStopListening() {},
  });
  Services.obs.addObserver(() => server?.close(), "quit-application");
  State.load().then(attachWindows, Cu.reportError);
}

const connections = new Set();

function accept(transport) {
  const input = transport.openInputStream(0, 0, 0);
  const output = transport.openOutputStream(Ci.nsITransport.OPEN_BLOCKING, 0, 0);
  const reader = Cc["@mozilla.org/binaryinputstream;1"].createInstance(Ci.nsIBinaryInputStream);
  reader.setInputStream(input);
  const pump = Cc["@mozilla.org/network/input-stream-pump;1"].createInstance(Ci.nsIInputStreamPump);
  pump.init(input, 0, 0, false);
  const connection = { transport, input, output, pump, bytes: [], answered: false };
  connections.add(connection);
  const finish = status => {
    if (connection.answered) {
      return;
    }
    connection.answered = true;
    respond(connection, status)
      .catch(log)
      .finally(() => {
        output.close();
        input.close();
        connections.delete(connection);
      });
  };
  pump.asyncRead({
    onStartRequest() {},
    onDataAvailable(_request, _stream, _offset, count) {
      const bytes = reader.readByteArray(count);
      connection.bytes.push(...bytes);
      if (bytes.includes(10)) {
        finish(Cr.NS_OK);
      }
    },
    onStopRequest(_request, status) {
      finish(status);
    },
  });
}

async function respond(connection, status) {
  let reply;
  try {
    if (!Components.isSuccessCode(status)) {
      throw new Error(`read failed with status ${status}`);
    }
    const request = JSON.parse(new TextDecoder().decode(new Uint8Array(connection.bytes)));
    reply = { result: (await handle(request.method, request.params ?? {})) ?? null };
  } catch (error) {
    log(error);
    reply = { error: String(error?.message ?? error) };
  }
  let text;
  try {
    text = JSON.stringify(reply);
  } catch (error) {
    log(error);
    text = JSON.stringify({ error: `the reply could not be serialized: ${error?.message ?? error}` });
  }
  const binary = Cc["@mozilla.org/binaryoutputstream;1"].createInstance(Ci.nsIBinaryOutputStream);
  binary.setOutputStream(connection.output);
  binary.writeByteArray(Array.from(new TextEncoder().encode(text)));
  binary.flush();
}

const handlers = {
  status: getStatus,
  "tab.get": getTabs,
  "tab.create": postTab,
  "tab.update": putTab,
  "tab.delete": deleteTabs,
  "tab.content": getTabContent,
  "tab.metadata": getTabMetadata,
  "folder.get": getFolders,
  "groups.reload": putGroups,
  "live.reload": putLive,
  "group.open": postGroupOpen,
  "workspace.get": getWorkspaces,
  "workspace.create": postWorkspace,
  "workspace.update": putWorkspace,
  "sidebar.get": getSidebar,
  "sidebar.update": putSidebar,
  "history.get": getHistory,
  "history.stats": getHistoryStats,
  "history.trails": getHistoryTrails,
  "history.import": postHistory,
  "history.delete": deleteHistory,
  "import.space": postImportSpace,
  "import.essentials": postImportEssentials,
  "project.reload": putProjects,
  "apps.get": getApps,
  "route.get": getRoutes,
  "route.create": postRoute,
  "route.delete": deleteRoute,
  "apps.update": putApps,
  eval: postEval,
  restart: postRestart,
};

function handle(method, params) {
  const handler = handlers[method];
  if (!handler) {
    throw new Error(`unknown method ${method}`);
  }
  return handler(params);
}

function getStatus() {
  const win = browserWindow();
  return {
    version,
    zen: Services.appinfo.version,
    windows: [...browserWindows()].length,
    tabs: [...browserWindows()].reduce((sum, w) => sum + realTabs(w).length, 0),
    space: win.gZenWorkspaces.activeWorkspace,
    projects: State.projects.length,
  };
}

function getTabs({ space }) {
  const tabs = [];
  for (const win of browserWindows()) {
    for (const tab of realTabs(win)) {
      if (!space || tab.getAttribute("zen-workspace-id") === space) {
        tabs.push(describeTab(tab));
      }
    }
  }
  return tabs;
}

async function postTab({ url, space, select }) {
  if (!url) {
    throw new Error("name the url to open");
  }
  const win = browserWindow();
  const tab = win.gBrowser.addTab(url, {
    triggeringPrincipal: Services.scriptSecurityManager.getSystemPrincipal(),
    inBackground: !select,
    userContextId: getSpaceContext(win, space),
  });
  if (space) {
    win.gZenWorkspaces.moveTabToWorkspace(tab, space);
  }
  if (select) {
    win.gBrowser.selectedTab = tab;
  }
  return describeTab(tab);
}

async function putTab({ id, select, space, pin, url }) {
  const found = getTab(id);
  const win = found.win;
  let tab = found.tab;
  if (space) {
    win.gZenWorkspaces.moveTabToWorkspace(tab, space);
    if (!tabLogin(tab)) {
      tab = putTabContext(win, tab, getSpaceContext(win, space));
    }
  }
  if (url) {
    tab.linkedBrowser.fixupAndLoadURIString(url, {
      triggeringPrincipal: Services.scriptSecurityManager.getSystemPrincipal(),
    });
  }
  if (pin === true) {
    win.gBrowser.pinTab(tab);
  }
  if (pin === false) {
    win.gBrowser.unpinTab(tab);
  }
  if (select) {
    const target = tab.getAttribute("zen-workspace-id");
    if (target && target !== win.gZenWorkspaces.activeWorkspace) {
      await win.gZenWorkspaces.changeWorkspaceWithID(target);
    }
    win.gBrowser.selectedTab = tab;
    win.focus();
  }
  return describeTab(tab);
}

function deleteTabs({ ids: list }) {
  const found = getNamedTabs(list);
  const removed = found.map(({ tab }) => describeTab(tab));
  for (const { win, tab } of found) {
    win.gBrowser.removeTab(tab);
  }
  return removed;
}

function pageExtractor(id) {
  const { tab } = getTab(id);
  const global = tab.linkedBrowser.browsingContext?.currentWindowGlobal;
  if (!global || tab.hasAttribute("pending")) {
    throw new Error(`tab ${id} is not loaded, select it first with tab update ${id} --select`);
  }
  return { tab, actor: global.getActor("PageExtractor") };
}

async function getTabContent({ id }) {
  const { tab, actor } = pageExtractor(id);
  const result = await actor.getText({});
  return { ...describeTab(tab), text: result?.text ?? "" };
}

async function getTabMetadata({ id }) {
  const { tab, actor } = pageExtractor(id);
  return { ...describeTab(tab), metadata: (await actor.getPageMetadata()) ?? {} };
}

function getFolders({ space }) {
  const folders = [];
  for (const win of browserWindows()) {
    for (const folder of win.document.querySelectorAll("zen-folder")) {
      if (!space || folder.getAttribute("zen-workspace-id") === space) {
        folders.push(describeFolder(folder));
      }
    }
  }
  return folders;
}

function describeSpace(space, active) {
  const profile = space.containerTabId ? lazy.ContextualIdentityService.getUserContextLabel(space.containerTabId) : null;
  return { id: space.uuid, name: space.name, icon: space.icon ?? null, profile, active: space.uuid === active };
}

function getWorkspaces() {
  const win = browserWindow();
  const active = win.gZenWorkspaces.activeWorkspace;
  return win.gZenWorkspaces.getWorkspaces().map(space => describeSpace(space, active));
}

async function postWorkspace({ name, icon, color }) {
  if (!name) {
    throw new Error("name the workspace");
  }
  const win = browserWindow();
  const space = await win.gZenWorkspaces.createAndSaveWorkspace(name, icon || undefined, true);
  putSpaceLook(win, win.gZenWorkspaces.getWorkspaceFromId(space.uuid), { color });
  return describeSpace(win.gZenWorkspaces.getWorkspaceFromId(space.uuid), win.gZenWorkspaces.activeWorkspace);
}

async function putWorkspace({ id, name, icon, select, color }) {
  const win = browserWindow();
  const space = win.gZenWorkspaces.getWorkspaceFromId(id);
  if (!space) {
    throw new Error(`no workspace with id ${id}, read the ids with workspace get`);
  }
  if (name || icon) {
    win.gZenWorkspaces.saveWorkspace({ ...space, name: name || space.name, icon: icon || space.icon });
  }
  putSpaceLook(win, win.gZenWorkspaces.getWorkspaceFromId(id), { color });
  if (select) {
    await win.gZenWorkspaces.changeWorkspaceWithID(id);
  }
  return describeSpace(win.gZenWorkspaces.getWorkspaceFromId(id), win.gZenWorkspaces.activeWorkspace);
}

function getSidebar() {
  const win = browserWindow();
  return {
    compact: win.gZenCompactModeManager.preference,
    expanded: Services.prefs.getBoolPref("zen.view.sidebar-expanded"),
    right: win.gZenCompactModeManager.sidebarIsOnRight,
  };
}

function putSidebar({ compact, expanded }) {
  const win = browserWindow();
  if (compact !== undefined) {
    win.gZenCompactModeManager.preference = compact;
  }
  if (expanded !== undefined) {
    Services.prefs.setBoolPref("zen.view.sidebar-expanded", expanded);
  }
  return getSidebar();
}

async function getHistory({ query = "", limit = 50, since = "" }) {
  const terms = query.split(/\s+/).filter(Boolean);
  const params = { limit: Math.max(1, Math.min(Number(limit) || 50, 5000)), since: since ? Date.parse(since) * 1000 : 0 };
  const where = terms.map((term, index) => {
    params[`t${index}`] = `%${term.replace(/[\\%_]/g, match => "\\" + match)}%`;
    return `(p.url LIKE :t${index} ESCAPE '\\' OR p.title LIKE :t${index} ESCAPE '\\')`;
  });
  const db = await lazy.PlacesUtils.promiseDBConnection();
  const rows = await db.execute(
    `SELECT p.url, p.title, count(v.id) AS visit_count, max(v.visit_date) AS last_visit_date FROM moz_places p
     JOIN moz_historyvisits v ON v.place_id = p.id AND v.visit_date >= :since
     WHERE p.hidden = 0 ${where.map(clause => "AND " + clause).join(" ")}
     GROUP BY p.id ORDER BY last_visit_date DESC LIMIT :limit`,
    params
  );
  return rows.map(row => ({
    url: row.getResultByName("url"),
    title: row.getResultByName("title") ?? "",
    visits: row.getResultByName("visit_count"),
    last_visit: new Date(row.getResultByName("last_visit_date") / 1000).toISOString(),
  }));
}

async function deleteHistory(request) {
  const urls = request.urls ?? [];
  const hosts = request.hosts ?? [];
  const prefixes = request.prefixes ?? [];
  const db = await lazy.PlacesUtils.promiseDBConnection();
  const doomed = new Set();
  for (const url of urls.filter(Boolean)) {
    if ((await db.execute(`SELECT 1 FROM moz_places WHERE url = :url`, { url })).length) {
      doomed.add(url);
    }
  }
  for (const prefix of prefixes.filter(Boolean)) {
    const rows = await db.execute(`SELECT url FROM moz_places WHERE lower(url) LIKE :prefix ESCAPE '\\'`, {
      prefix: `${prefix.toLowerCase().replace(/[\\%_]/g, match => "\\" + match)}%`,
    });
    rows.forEach(row => doomed.add(row.getResultByName("url")));
  }
  let removed = doomed.size;
  if (doomed.size) {
    await lazy.PlacesUtils.history.remove([...doomed]);
  }
  for (const host of hosts.filter(Boolean)) {
    let base = host;
    try {
      base = Services.eTLD.getBaseDomainFromHost(host);
    } catch {}
    const rows = await db.execute(`SELECT count(*) AS n FROM moz_places WHERE rev_host LIKE :rev`, { rev: `${[...base].reverse().join("")}.%` });
    removed += rows[0]?.getResultByName("n") ?? 0;
    await lazy.PlacesUtils.history.removeByFilter({ host: `.${base}` });
  }
  return { removed };
}

async function getHistoryTrails({ urls = [], since = "" }) {
  const start = since ? Date.parse(since) * 1000 : 0;
  const db = await lazy.PlacesUtils.promiseDBConnection();
  const step = row => ({
    url: row.getResultByName("url"),
    title: row.getResultByName("title") ?? "",
    visited_at: new Date(row.getResultByName("visit_date") / 1000).toISOString(),
  });
  const trails = {};
  for (const url of urls) {
    const latest = await db.execute(
      `SELECT v.id, v.from_visit FROM moz_historyvisits v JOIN moz_places p ON p.id = v.place_id WHERE p.url = :url ORDER BY v.visit_date DESC LIMIT 1`,
      { url }
    );
    if (!latest.length) {
      trails[url] = [];
      continue;
    }
    const trail = [];
    const seen = new Set();
    const fresh = entry => {
      const parsed = URL.parse(entry.url);
      const keys = [parsed ? parsed.origin + parsed.pathname.replace(/\/$/, "") : entry.url, entry.title && `${parsed?.host}|${entry.title}`].filter(Boolean);
      if (keys.some(key => seen.has(key))) {
        return false;
      }
      keys.forEach(key => seen.add(key));
      return true;
    };
    fresh({ url, title: "" });
    let from = latest[0].getResultByName("from_visit");
    for (let depth = 0; from && depth < 4; depth++) {
      const rows = await db.execute(
        `SELECT v.from_visit, v.visit_date, p.url, p.title FROM moz_historyvisits v JOIN moz_places p ON p.id = v.place_id WHERE v.id = :id`,
        { id: from }
      );
      if (!rows.length) {
        break;
      }
      const entry = step(rows[0]);
      if (fresh(entry)) {
        trail.unshift({ ...entry, way: "from" });
      }
      from = rows[0].getResultByName("from_visit");
    }
    const children = await db.execute(
      `SELECT DISTINCT p.url, p.title, v.visit_date FROM moz_historyvisits v JOIN moz_places p ON p.id = v.place_id
       WHERE v.from_visit = :id AND p.url != :url ORDER BY v.visit_date LIMIT 5`,
      { id: latest[0].getResultByName("id"), url }
    );
    for (const row of children) {
      const entry = step(row);
      if (fresh(entry)) {
        trail.push({ ...entry, way: "to" });
      }
    }
    const parsed = URL.parse(url);
    if (parsed) {
      const segments = parsed.pathname.split("/").filter(Boolean).slice(0, 2);
      const base = `${parsed.origin}/${segments.join("/")}`.replace(/\/$/, "");
      const subpages = await db.execute(
        `SELECT p.url, p.title, p.last_visit_date AS visit_date FROM moz_places p
         WHERE p.url LIKE :base ESCAPE '\\' AND p.hidden = 0 AND p.last_visit_date >= :start
         ORDER BY p.visit_count DESC, p.last_visit_date DESC LIMIT 12`,
        { base: `${base.replace(/[\\%_]/g, match => "\\" + match)}/%`, start }
      );
      for (const row of subpages) {
        const entry = step(row);
        if (trail.filter(item => item.way === "sub").length < 5 && fresh(entry)) {
          trail.push({ ...entry, way: "sub" });
        }
      }
    }
    trails[url] = trail.filter(entry => Date.parse(entry.visited_at) * 1000 >= start);
  }
  return trails;
}

async function getHistoryStats({ urls = [] }) {
  const db = await lazy.PlacesUtils.promiseDBConnection();
  const stats = [];
  for (let start = 0; start < urls.length; start += 400) {
    const chunk = urls.slice(start, start + 400);
    const params = Object.fromEntries(chunk.map((url, index) => [`u${index}`, url]));
    const rows = await db.execute(
      `SELECT p.url, p.title, p.visit_count, p.last_visit_date,
        (SELECT group_concat(v.visit_date / 1000, ",") FROM (SELECT visit_date FROM moz_historyvisits WHERE place_id = p.id ORDER BY visit_date DESC LIMIT 20) v) AS recent
       FROM moz_places p WHERE p.url IN (${chunk.map((_, index) => `:u${index}`).join(",")})`,
      params
    );
    for (const row of rows) {
      stats.push({
        url: row.getResultByName("url"),
        title: row.getResultByName("title") ?? "",
        visits: row.getResultByName("visit_count") ?? 0,
        last_visit: row.getResultByName("last_visit_date") ? new Date(row.getResultByName("last_visit_date") / 1000).toISOString() : null,
        recent: (row.getResultByName("recent") ?? "").split(",").filter(Boolean).map(value => new Date(Number(value)).toISOString()),
      });
    }
  }
  return stats;
}

async function postHistory({ pages }) {
  const now = Date.now();
  const infos = (pages ?? [])
    .filter(page => /^https?:/.test(page.url))
    .map(page => ({
      url: page.url,
      title: page.title || undefined,
      visits: (page.visits ?? [])
        .filter(date => date > 0 && date <= now)
        .map(date => ({ date: new Date(date), transition: lazy.PlacesUtils.history.TRANSITIONS.LINK })),
    }))
    .filter(info => info.visits.length > 0);
  if (infos.length === 0) {
    return { imported: 0, failed: 0 };
  }
  let failed = 0;
  await lazy.PlacesUtils.history.insertMany(infos, null, () => failed++);
  return { imported: infos.length - failed, failed };
}

function postImportEssentials({ pages = [] }) {
  const win = browserWindow();
  const open = new Set(
    realTabs(win)
      .filter(tab => tab.hasAttribute("zen-essential"))
      .map(tabUrl)
  );
  let added = 0;
  for (const page of pages) {
    if (!/^https?:/.test(page.url) || open.has(page.url)) {
      continue;
    }
    open.add(page.url);
    const tab = postLazyTab(win, page, null, { pinned: false });
    if (win.gZenPinnedTabManager.addToEssentials(tab)) {
      added++;
    }
  }
  return { essentials: added };
}

async function postImportSpace({ name, icon, tabs = [], loose = [] }) {
  const win = browserWindow();
  let space = win.gZenWorkspaces.getWorkspaces().find(entry => entry.name === name);
  if (!space) {
    space = await win.gZenWorkspaces.createAndSaveWorkspace(name, icon || undefined, true);
  }
  const open = new Set(
    realTabs(win)
      .filter(tab => tab.getAttribute("zen-workspace-id") === space.uuid)
      .map(tabUrl)
  );
  const fresh = pages => (pages ?? []).filter(page => /^https?:/.test(page.url) && !open.has(page.url) && open.add(page.url));
  const pinned = fresh(tabs).map(page => postLazyTab(win, page, space.uuid));
  const unpinned = fresh(loose).map(page => postLazyTab(win, page, space.uuid, { pinned: false }));
  return { space: space.uuid, name: space.name, pinned: pinned.length, loose: unpinned.length, folders: 0 };
}

async function putLive() {
  await State.loadLive();
  refreshWindows();
  return { live: State.live.length };
}

async function putGroups() {
  await State.loadGroups();
  refreshWindows();
  return { groups: State.groups.length };
}

async function postGroupOpen({ id, url }) {
  await State.loadGroups();
  const group = State.groups.find(entry => entry.id === id);
  if (!group) {
    throw new Error(`no group with id ${id}, read the ids with group get`);
  }
  return describeTab(await postGroupTab(browserWindow(), group, url || undefined));
}

function describeRoute(route) {
  const win = browserWindow();
  const space = win.gZenWorkspaces.getWorkspaceFromId(route.openIn);
  return { id: route.id, match: route.matchType, reference: route.reference, space: route.openIn, space_name: space?.name ?? null };
}

function getRoutes() {
  return lazy.gZenSpaceRoutingManager.getAllRoutes().filter(route => route.reference).map(describeRoute);
}

function postRoute({ reference, match = "contains", space }) {
  if (!reference) {
    throw new Error("name the reference the url must contain, equal or match");
  }
  if (!["contains", "equal-to", "regex"].includes(match)) {
    throw new Error("match takes contains, equal-to or regex");
  }
  const win = browserWindow();
  if (!win.gZenWorkspaces.getWorkspaceFromId(space)) {
    throw new Error(`no workspace with id ${space}, read the ids with workspace get`);
  }
  if (match === "regex") {
    new RegExp(reference);
  }
  const routing = lazy.gZenSpaceRoutingManager;
  const route = routing.createNewRoute();
  routing.updateRoute({ ...route, reference, matchType: match, openIn: space });
  routing.saveRoutes();
  return describeRoute(routing.getRoute(route.id));
}

function deleteRoute({ id }) {
  const routing = lazy.gZenSpaceRoutingManager;
  const route = routing.getRoute(id);
  if (!route) {
    throw new Error(`no route with id ${id}, read the ids with space route get`);
  }
  routing.removeRoute(id);
  routing.saveRoutes();
  return describeRoute(route);
}

function getApps() {
  return {
    fit: Services.prefs.getStringPref("slybrowser.apps.fit", "width"),
    size: Number(Services.prefs.getStringPref("slybrowser.apps.size", "5"))
  };
}

function putApps({ fit, size }) {
  if (fit !== undefined && fit !== "width" && fit !== "height") {
    throw new Error("fit takes width or height");
  }
  if (fit) {
    Services.prefs.setStringPref("slybrowser.apps.fit", fit);
  }
  if (size) {
    Services.prefs.setStringPref("slybrowser.apps.size", String(Math.max(2, Math.min(Number(size), 20))));
  }
  refreshWindows();
  return getApps();
}

async function putProjects() {
  await State.loadProjects();
  refreshWindows();
  return { projects: State.projects.length };
}

async function postEval({ code }) {
  if (!code) {
    throw new Error("pass the code to run");
  }
  const win = browserWindow();
  const sandbox = Cu.Sandbox(Services.scriptSecurityManager.getSystemPrincipal(), {
    sandboxPrototype: win,
    wantXrays: false,
    sandboxName: "slybrowser eval",
  });
  Object.assign(sandbox, { Services, tabId, getTab: id => getTab(id).tab, folderTabs });
  const value = await Cu.evalInSandbox(`(async () => {\n${code}\n})()`, sandbox);
  if (value === undefined) {
    return null;
  }
  try {
    return JSON.parse(JSON.stringify(value));
  } catch {
    return String(value);
  }
}

function postRestart() {
  Services.tm.dispatchToMainThread(() => {
    Services.appinfo.invalidateCachesOnRestart();
    Services.startup.quit(Ci.nsIAppStartup.eAttemptQuit | Ci.nsIAppStartup.eRestart);
  });
  return { restarting: true };
}
