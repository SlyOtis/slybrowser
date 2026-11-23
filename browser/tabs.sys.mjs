import { State } from "resource://slybrowser/state.sys.mjs";

const lazy = {};
ChromeUtils.defineESModuleGetters(lazy, {
  SessionStore: "moz-src:///browser/components/sessionstore/SessionStore.sys.mjs",
  ContextualIdentityService: "moz-src:///toolkit/components/contextualidentity/ContextualIdentityService.sys.mjs",
});

const ids = new WeakMap();
let nextId = 1;

export function isPopup(win) {
  return !win.toolbar?.visible || /\btoolbar\b/.test(win.document.documentElement.getAttribute("chromehidden") ?? "");
}

export function browserWindow() {
  const newest = Services.wm.getMostRecentWindow("navigator:browser");
  const win = newest && !isPopup(newest) ? newest : [...browserWindows()].filter(other => !isPopup(other)).pop() ?? newest;
  if (!win) {
    throw new Error("no browser window is open");
  }
  return win;
}

export function* browserWindows() {
  yield* Services.wm.getEnumerator("navigator:browser");
}

export function tabId(tab) {
  let id = ids.get(tab);
  if (!id) {
    id = String(nextId++);
    ids.set(tab, id);
  }
  return id;
}

export function realTabs(win) {
  return [...win.gZenWorkspaces.allStoredTabs].filter(tab => !tab.hasAttribute("zen-empty-tab") && !tab.hasAttribute("zen-glance-tab"));
}

export function tabUrl(tab) {
  const url = tab.linkedBrowser?.currentURI?.spec ?? "";
  if (url === "about:blank" && tab.hasAttribute("pending")) {
    return lazy.SessionStore.getLazyTabValue(tab, "url") ?? url;
  }
  return url;
}

export function folderOf(tab) {
  return tab.group?.isZenFolder ? tab.group : null;
}

export function describeTab(tab) {
  const url = tabUrl(tab);
  const folder = folderOf(tab);
  return {
    id: tabId(tab),
    title: tab.label,
    url,
    space: tab.getAttribute("zen-workspace-id") || null,
    folder: folder?.id ?? null,
    folder_label: folder?.label ?? null,
    project: State.projectOf(url),
    pinned: tab.pinned,
    essential: tab.hasAttribute("zen-essential"),
    selected: tab.selected,
    loaded: !tab.hasAttribute("pending"),
    last_accessed: new Date(tab.lastAccessed).toISOString(),
    login: tabLogin(tab),
  };
}

export function tabLogin(tab) {
  const context = tab.userContextId;
  const profiles = new Set(browserWindow().gZenWorkspaces.getWorkspaces().map(space => space.containerTabId));
  return context && !profiles.has(context) ? lazy.ContextualIdentityService.getUserContextLabel(context) : "";
}

export const loginColors = ["turquoise", "yellow", "pink", "orange", "green", "purple", "blue", "red"];

export function loginContext(name) {
  const service = lazy.ContextualIdentityService;
  const identities = service.getPublicIdentities();
  const found = identities.find(identity => service.getUserContextLabel(identity.userContextId) === name);
  const used = new Set(identities.map(identity => identity.color));
  return (found ?? service.create(name, "fingerprint", loginColors.find(color => !used.has(color)) ?? "toolbar")).userContextId;
}

export function getSpaceContext(win, space) {
  return win.gZenWorkspaces.getWorkspaceFromId(space || win.gZenWorkspaces.activeWorkspace)?.containerTabId ?? 0;
}

export function putTabContext(win, tab, userContextId) {
  if (tab.userContextId === userContextId) {
    return tab;
  }
  const state = JSON.parse(lazy.SessionStore.getTabState(tab));
  state.userContextId = userContextId;
  state.zenDefaultUserContextId = userContextId === getSpaceContext(win, tab.getAttribute("zen-workspace-id"));
  const moved = win.gBrowser.addTrustedTab("about:blank", { createLazyBrowser: true, inBackground: true, skipAnimation: true, userContextId });
  lazy.SessionStore.setTabState(moved, JSON.stringify(state));
  if (tab.group) {
    win.gBrowser.moveTabToExistingGroup(moved, tab.group);
  } else {
    win.gBrowser.moveTabAfter(moved, tab);
  }
  for (const name of ["sly-page", "sly-group"]) {
    if (tab.hasAttribute(name)) {
      moved.setAttribute(name, tab.getAttribute(name));
    }
  }
  if (tab.selected) {
    win.gBrowser.selectedTab = moved;
  }
  win.gBrowser.removeTab(tab);
  return moved;
}

export function getTab(id) {
  for (const win of browserWindows()) {
    for (const tab of realTabs(win)) {
      if (tabId(tab) === String(id)) {
        return { win, tab };
      }
    }
  }
  throw new Error(`no tab with id ${id}, read the ids with tab get`);
}

export function getNamedTabs(list) {
  if (!Array.isArray(list) || list.length === 0) {
    throw new Error("name at least one tab id");
  }
  return list.map(id => getTab(id));
}

export function folderTabs(folder) {
  return folder.tabs.filter(tab => !tab.hasAttribute("zen-empty-tab"));
}

export function describeFolder(folder) {
  return {
    id: folder.id,
    label: folder.label,
    space: folder.getAttribute("zen-workspace-id") || null,
    collapsed: folder.collapsed,
    tabs: folderTabs(folder).map(tabId),
  };
}

export function postLazyTab(win, { url, title }, space, { pinned = true } = {}) {
  const tab = win.gBrowser.addTrustedTab(url, {
    createLazyBrowser: true,
    inBackground: true,
    skipAnimation: true,
    noInitialLabel: true,
    lazyTabTitle: title || undefined,
    userContextId: getSpaceContext(win, space),
  });
  if (space) {
    win.gZenWorkspaces.moveTabToWorkspace(tab, space);
  }
  if (pinned) {
    win.gBrowser.pinTab(tab);
  }
  return tab;
}


export async function postOpenPage(win, url, space, { userContextId = getSpaceContext(win, space) } = {}) {
  const open = realTabs(win).find(tab => (tab.getAttribute("sly-page") === url || tabUrl(tab) === url) && tab.userContextId === userContextId && (!space || tab.getAttribute("zen-workspace-id") === space));
  if (open) {
    win.gBrowser.selectedTab = open;
    return open;
  }
  const tab = win.gBrowser.addTrustedTab(url, { inBackground: false, userContextId });
  if (space) {
    win.gZenWorkspaces.moveTabToWorkspace(tab, space);
  }
  win.gBrowser.selectedTab = tab;
  return tab;
}
export const dimmedColors = {
  red: [150, 72, 80],
  purple: [104, 82, 148],
  blue: [66, 96, 146],
  green: [70, 122, 92],
  turquoise: [62, 124, 128],
  yellow: [150, 132, 70],
  orange: [156, 102, 66],
  pink: [150, 84, 120],
};

function putProfile(space, color) {
  const service = lazy.ContextualIdentityService;
  const own = space.containerTabId ? service.getPublicIdentityFromId(space.containerTabId) : null;
  if (own) {
    if (service.getUserContextLabel(own.userContextId) !== space.name || (color && own.color !== color)) {
      service.update(own.userContextId, space.name, own.icon, color || own.color);
    }
    return own.userContextId;
  }
  return service.create(space.name, "fingerprint", color || "blue").userContextId;
}

export const themeOpacity = 0.2;

export function themeFromColors(colors) {
  const spots = [
    { x: 0, y: 0 },
    { x: 220, y: 120 },
    { x: 90, y: 260 },
  ];
  return {
    type: "gradient",
    gradientColors: colors.slice(0, 3).map((rgb, index) => ({
      c: rgb,
      isCustom: false,
      algorithm: colors.length > 1 ? "analogous" : "",
      isPrimary: index === 0,
      lightness: 35,
      position: spots[index],
      type: "explicit-lightness",
    })),
    opacity: themeOpacity,
    texture: 0,
  };
}

export function putSpaceLook(win, space, { color } = {}) {
  if (color && !dimmedColors[color]) {
    throw new Error(`color ${color} is not one of ${Object.keys(dimmedColors).join(", ")}`);
  }
  const changed = { ...space, containerTabId: putProfile(space, color) };
  if (color) {
    changed.theme = themeFromColors([dimmedColors[color]]);
  }
  if (changed.containerTabId !== space.containerTabId || color) {
    win.gZenWorkspaces.saveWorkspace(changed);
  }
  if (color && win.gZenWorkspaces.activeWorkspace === space.uuid) {
    win.gZenThemePicker.onWorkspaceChange(win.gZenWorkspaces.getWorkspaceFromId(space.uuid));
  }
}
