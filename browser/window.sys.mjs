import { State, logError, paths, runSlyb as runSlybCommand } from "resource://slybrowser/state.sys.mjs";
import { browserWindows, dimmedColors, getSpaceContext, isPopup, loginColors, loginContext, postOpenPage, putSpaceLook, putTabContext, realTabs, tabLogin, tabUrl, themeFromColors, themeOpacity } from "resource://slybrowser/tabs.sys.mjs";

const lazy = {};
ChromeUtils.defineESModuleGetters(lazy, {
  PlacesUtils: "resource://gre/modules/PlacesUtils.sys.mjs",
  setTimeout: "resource://gre/modules/Timer.sys.mjs",
  clearTimeout: "resource://gre/modules/Timer.sys.mjs",
  ContextualIdentityService: "moz-src:///toolkit/components/contextualidentity/ContextualIdentityService.sys.mjs",
  SessionStore: "moz-src:///browser/components/sessionstore/SessionStore.sys.mjs",
  DevToolsShim: "chrome://devtools-startup/content/DevToolsShim.sys.mjs",
  gZenSpaceRoutingManager: "resource:///modules/zen/spacerouting/ZenSpaceRoutingManager.sys.mjs",
});

const sheet = "resource://slybrowser/arc.css";
const modesPref = "slybrowser.card-modes";
const spaceIconsPref = "slybrowser.space-icons";
const cardsPref = "slybrowser.card-state";
const rowLimit = 8;
const attached = new WeakSet();
const expanded = new Set();
const thinking = new Set();
const opened = new Set();
const groupIcons = new Map();
const colorCache = new Map();
const humanCache = new Map();
const looseOrder = new Map();
const groupOrder = new Map();
const searchEngines = ["https://www.google.com/search?%", "https://duckduckgo.com/?%", "https://www.bing.com/search?%", "https://search.brave.com/search?%"];

export function attachWindows() {
  for (const win of browserWindows()) {
    attach(win);
  }
  Services.obs.addObserver(subject => attach(subject), "browser-delayed-startup-finished");
}

export function refreshWindows() {
  for (const win of [...browserWindows()].filter(win => attached.has(win))) {
    tagTabs(win);
    renderCards(win);
    renderApps(win);
  }
}

export async function postGroupTab(win, group, url, given) {
  const userContextId = group.login ? loginContext(group.login) : getSpaceContext(win, group.space);
  if (win.gZenWorkspaces.activeWorkspace !== group.space && win.gZenWorkspaces.getWorkspaceFromId(group.space)) {
    await win.gZenWorkspaces.changeWorkspaceWithID(group.space);
  }
  const page = url && group.sections.flatMap(section => section.pages).find(page => page.url === url);
  let tab;
  if (given && given.userContextId === userContextId) {
    tab = given;
    if (tab.getAttribute("zen-workspace-id") !== group.space) {
      win.gZenWorkspaces.moveTabToWorkspace(tab, group.space);
    }
  } else if (url) {
    tab = await postOpenPage(win, url, group.space, { userContextId });
    if (given) {
      win.gBrowser.removeTab(given);
    }
  } else {
    tab = win.gBrowser.addTrustedTab(win.BROWSER_NEW_TAB_URL, { inBackground: false, userContextId });
    win.gZenWorkspaces.moveTabToWorkspace(tab, group.space);
  }
  if (page && !pageTaken(win, tab, page.url, group.space)) {
    tagTab(tab, "sly-page", page.url);
  } else {
    tagTab(tab, "sly-group", group.id);
  }
  win.gBrowser.selectedTab = tab;
  opened.add(`group:${group.id}`);
  expanded.add(`group:${group.id}`);
  win._slySchedule?.();
  return tab;
}

function attach(win) {
  if (attached.has(win) || isPopup(win) || !win.gBrowserInit?.delayedStartupFinished || !win.gZenWorkspaces) {
    return;
  }
  attached.add(win);
  win.windowUtils.loadSheetUsingURIString(sheet, win.windowUtils.AUTHOR_SHEET);
  const doc = win.document;
  attachTips(win);

  const schedule = () => {
    win.clearTimeout(win._slyRender);
    win._slyRender = win.setTimeout(() => {
      tagTabs(win);
      renderViews(win);
      renderPage(win);
      renderCards(win).then(() => {
        renderFooter(win);
        return applyTheme(win);
      }).catch(logError);
      renderApps(win);
    }, 150);
  };
  const appsRow = element(doc, "hbox", "sly-apps", { id: "sly-apps" });
  const carriesApp = event => heldPage && /^https?:/.test(heldPage.url) && (event.dataTransfer.types.includes(dragType) || event.dataTransfer.types.includes(appType));
  appsRow.addEventListener("dragover", event => {
    if (!carriesApp(event)) {
      return;
    }
    event.preventDefault();
    event.dataTransfer.dropEffect = event.dataTransfer.types.includes(appType) ? "move" : "copy";
    appsRow.setAttribute("sly-drop", "true");
    placeAppHolder(win, appsRow, event);
  });
  appsRow.addEventListener("dragleave", event => {
    if (!appsRow.contains(event.relatedTarget)) {
      appsRow.removeAttribute("sly-drop");
      appsRow.querySelector(".sly-app[placeholder]")?.remove();
    }
  });
  appsRow.addEventListener("drop", event => {
    if (!carriesApp(event)) {
      return;
    }
    event.preventDefault();
    appsRow.removeAttribute("sly-drop");
    const holder = appsRow.querySelector(".sly-app[placeholder]");
    const at = holder ? [...appsRow.children].filter(tile => tile.hasAttribute("pinned") && !tile.hasAttribute("dragging") && tile.compareDocumentPosition(holder) & tile.DOCUMENT_POSITION_FOLLOWING).length : undefined;
    pinApp(win, heldPage, true, at);
  });
  doc.getElementById("nav-bar").before(appsRow);
  const keepApps = () => {
    const navBar = doc.getElementById("nav-bar");
    const apps = doc.getElementById("sly-apps");
    if (navBar && apps && navBar.previousElementSibling !== apps) {
      navBar.before(apps);
    }
  };
  const appsObserver = new win.MutationObserver(keepApps);
  appsObserver.observe(doc.getElementById("navigator-toolbox"), { childList: true });
  appsObserver.observe(doc.getElementById("titlebar"), { childList: true });
  const views = [
    ["groups", "Groups view", "Show this space as group cards"],
    ["projects", "Projects view", "Show this space as project cards"],
  ].map(([mode, label, tooltip]) => {
    const button = element(doc, "toolbarbutton", `toolbarbutton-1 chromeclass-toolbar-additional sly-view sly-view-${mode}`, { id: `sly-view-${mode}`, label, tooltiptext: tooltip });
    button.addEventListener("command", () => {
      const space = win.gZenWorkspaces.activeWorkspace;
      const modes = readPref(modesPref);
      modes[space] = mode;
      writePref(modesPref, modes);
      const workspace = doc.getElementById(space);
      if (workspace) {
        applyMode(workspace, mode);
      }
      renderViews(win);
      renderCards(win);
    });
    return button;
  });
  const addSpace = element(doc, "toolbarbutton", "subviewbutton subviewbutton-iconic sly-add-space", { id: "sly-add-space", label: "New workspace", tooltiptext: "Create a workspace with its own profile and domain" });
  addSpace.addEventListener("command", () => openSpaceDialog(win));
  const welcome = element(doc, "toolbarbutton", "toolbarbutton-1 chromeclass-toolbar-additional sly-view sly-welcome", { id: "sly-welcome", label: "Welcome", tooltiptext: "Go to the welcome page" });
  const openWelcome = () => {
    const page = win.BROWSER_NEW_TAB_URL;
    const open = realTabs(win).find(tab => tabUrl(tab) === page && tab.getAttribute("zen-workspace-id") === win.gZenWorkspaces.activeWorkspace);
    if (open) {
      win.gBrowser.selectedTab = open;
    } else {
      win.openTrustedLinkIn(page, "tab");
    }
  };
  welcome.addEventListener("command", openWelcome);
  const leaveEmpty = () =>
    win.setTimeout(() => {
      if (!win.gBrowser.selectedTab?.hasAttribute("zen-empty-tab")) {
        return;
      }
      const last = realTabs(win)
        .filter(tab => !tab.hasAttribute("zen-empty-tab") && tabUrl(tab) !== win.BROWSER_NEW_TAB_URL && tab.getAttribute("zen-workspace-id") === win.gZenWorkspaces.activeWorkspace)
        .sort((a, b) => b.lastAccessed - a.lastAccessed)[0];
      if (last) {
        win.gBrowser.selectedTab = last;
      } else {
        openWelcome();
      }
    }, 400);
  win.addEventListener("TabSelect", leaveEmpty);
  win.addEventListener("ZenWorkspacesUIUpdate", leaveEmpty);
  leaveEmpty();
  const sync = element(doc, "toolbarbutton", "toolbarbutton-1 sly-view sly-sync", { id: "sly-sync", label: "Sync projects", tooltiptext: "Find the repositories and Claude sessions again and rebuild the projects" });
  sync.dataset.tooltip = "Find the repositories and Claude sessions again and rebuild the projects";
  sync.addEventListener("command", () => runSlyb(win, ["project", "sync"], sync, null));
  const reload = element(doc, "toolbarbutton", "toolbarbutton-1 sly-view sly-reload", { id: "sly-reload", label: "Reload", tooltiptext: "Reload this page" });
  reload.addEventListener("command", event => win.BrowserCommands.reload(event));
  const toggle = element(doc, "toolbaritem", "sly-toggle", { id: "sly-toggle", align: "center" });
  toggle.append(...views);
  const tools = [welcome, toggle, sync, reload];
  for (const id of ["back-button", "forward-button"]) {
    doc.getElementById(id)?.setAttribute("overflows", "false");
  }
  const keepTools = () => {
    let previous = doc.getElementById("zen-toggle-compact-mode");
    for (const tool of tools) {
      if (previous && tool.parentNode === previous.parentNode) {
        if (previous.nextElementSibling !== tool) {
          previous.after(tool);
        }
        previous = tool;
      }
    }
  };
  doc.getElementById("zen-toggle-compact-mode")?.after(...tools);
  const headings = { "sly-toggle": "View", "sly-sync": "Projects", "sly-reload": "Page" };
  doc.getElementById("widget-overflow")?.addEventListener("popupshowing", () => {
    const list = doc.getElementById("widget-overflow-list");
    if (list && !doc.getElementById("sly-overflow-top")) {
      const top = element(doc, "vbox", "sly-overflow-top", { id: "sly-overflow-top" });
      const unsorted = [
        ["Sort them into groups", [], "Place every unsorted page in the group it belongs in, making new groups where several share a theme"],
        ["Sort with my history too", ["--history"], "Also place the pages you visited more than once that no group holds"],
        ["Sort by prompt…", ["--prompt"], "Say how to sort or relate the unsorted pages"],
        ["Preview the sorting", ["--plan"], "Show where each unsorted page would go and change nothing"],
      ].map(([label, extra, tooltip]) => {
        const item = element(doc, "toolbarbutton", "subviewbutton subviewbutton-iconic sly-sort-unsorted", { label, tooltiptext: tooltip });
        item.addEventListener("command", () => {
          doc.getElementById("widget-overflow")?.hidePopup();
          const space = win.gZenWorkspaces.activeWorkspace;
          const args = ["organize", "--workspace", space, ...extra];
          if (extra[0] === "--prompt") {
            openPromptDialog(win, { title: "Sort the unsorted pages", hint: "Say how to sort or relate them. It comes before the organization rules.", placeholder: "Such as by client, newest work first", action: "Sort" }, text => runSlyb(win, [...args, text], item, `open:${space}`));
          } else {
            runSlyb(win, args, item, `open:${space}`);
          }
        });
        return item;
      });
      top.append(element(doc, "label", "sly-overflow-header", { value: "Workspaces" }), addSpace, element(doc, "toolbarseparator"), element(doc, "label", "sly-overflow-header", { value: "Unsorted pages" }), ...unsorted, element(doc, "toolbarseparator"));
      list.before(top);
    }
    list?.querySelectorAll(".sly-overflow-header").forEach(header => header.remove());
    for (const [id, heading] of Object.entries(headings)) {
      const item = doc.getElementById(id);
      if (list && item?.parentNode === list) {
        const header = element(doc, "label", "sly-overflow-header", { value: heading });
        if (win.getComputedStyle(list).flexDirection === "column-reverse") {
          item.after(header);
        } else {
          item.before(header);
        }
      }
    }
  });
  const target = doc.getElementById("zen-sidebar-top-buttons-customization-target");
  if (target) {
    new win.MutationObserver(keepTools).observe(target, { childList: true });
  }
  const page = element(doc, "hbox", "sly-page", { id: "sly-page", align: "center" });
  const pageCopy = element(doc, "toolbarbutton", "toolbarbutton-1 sly-page-copy", { tooltiptext: "Copy the address of this page" });
  pageCopy.addEventListener("command", event => {
    stopEvent(event);
    const url = win.gBrowser.currentURI?.spec ?? "";
    Cc["@mozilla.org/widget/clipboardhelper;1"].getService(Ci.nsIClipboardHelper).copyString(url);
    toast(win, pageCopy, "Copied the address");
  });
  pageCopy.dataset.tooltip = "Copy the address of this page";
  const pageClear = element(doc, "toolbarbutton", "toolbarbutton-1 sly-page-clear", { tooltiptext: "Clear the cookies, site data and cache of this site" });
  pageClear.dataset.tooltip = "Clear the cookies, site data and cache of this site";
  pageClear.addEventListener("command", event => {
    stopEvent(event);
    openClearDialog(win, pageClear);
  });
  const pageInspect = element(doc, "toolbarbutton", "toolbarbutton-1 sly-page-inspect", { tooltiptext: "Open the inspector for this page" });
  pageInspect.dataset.tooltip = "Open the inspector for this page";
  pageInspect.addEventListener("command", event => {
    stopEvent(event);
    Promise.resolve(lazy.DevToolsShim.showToolboxForTab(win.gBrowser.selectedTab, { toolId: "inspector" })).catch(logError);
  });
  page.append(
    element(doc, "image", "sly-page-icon"),
    element(doc, "vbox", "sly-page-text", { flex: "1" }),
    pageInspect,
    pageClear,
    pageCopy
  );
  for (const event of ["mousedown", "click", "dblclick"]) {
    page.addEventListener(event, stopEvent);
  }
  doc.getElementById("urlbar-container")?.append(page);
  const urlbarBackground = doc.querySelector("#urlbar > .urlbar-background");
  if (urlbarBackground) {
    doc.documentElement.style.setProperty("--sly-urlbar-background", win.getComputedStyle(urlbarBackground).backgroundColor);
  }
  win._slySchedule = schedule;
  let sticking = false;
  doc.addEventListener(
    "scroll",
    () => {
      if (sticking) {
        return;
      }
      sticking = true;
      win.requestAnimationFrame(() => {
        sticking = false;
        for (const head of doc.querySelectorAll(".sly-card:not([plain]):not([folded]) > .sly-card-head")) {
          const body = head.parentNode.querySelector(":scope > .sly-card-body");
          if (!body) {
            continue;
          }
          const covered = head.getBoundingClientRect().bottom - body.getBoundingClientRect().top;
          const offset = head.getBoundingClientRect().top - head.parentNode.getBoundingClientRect().top;
          body.style.clipPath = covered > 0.5 ? `inset(${covered}px 0 0 0)` : "";
          head.parentNode.style.clipPath = offset > 0.5 ? `inset(${offset}px 0 0 0 round 12px)` : "";
        }
      });
    },
    { capture: true, passive: true }
  );
  const outsideCards = record => !record.target.closest?.(".sly-cards") && !record.target.classList?.contains("sly-cards");
  const observer = new win.MutationObserver(records => {
    if (records.some(outsideCards) && decorateSpaces(win)) {
      schedule();
    }
  });
  observer.observe(doc.getElementById("tabbrowser-tabs"), { childList: true, subtree: true });
  new win.MutationObserver(records => {
    const moved = new Set(records.map(record => record.target).filter(tab => win.gBrowser.isTab(tab)));
    win.setTimeout(() => {
      for (const tab of moved) {
        const space = win.gZenWorkspaces.getWorkspaceFromId(tab.getAttribute("zen-workspace-id"));
        if (!tab.isConnected || !space || tab.hasAttribute("zen-essential") || tab.hasAttribute("zen-glance-tab") || tab.hasAttribute("zen-empty-tab") || tabLogin(tab) || groupOfTab(tab)?.login) {
          continue;
        }
        putTabContext(win, tab, space.containerTabId ?? 0);
      }
    }, 0);
  }).observe(doc.getElementById("tabbrowser-tabs"), { attributes: true, attributeFilter: ["zen-workspace-id"], subtree: true });
  decorateSpaces(win);

  for (const event of ["TabOpen", "TabClose", "TabAttrModified", "TabSelect", "ZenWorkspacesUIUpdate"]) {
    win.addEventListener(event, schedule);
  }
  win.addEventListener("TabOpen", event => {
    const opener = event.target.openerTab;
    if (opener && !event.target.pinned && !win.gURLBar.hasAttribute("zen-newtab")) {
      event.target._slyOpener = opener;
    }
  });
  win.addEventListener(
    "TabClose",
    event => {
      const order = looseOrder.get(event.target.getAttribute("zen-workspace-id"));
      if (order?.has(event.target)) {
        lazy.SessionStore.setCustomTabValue(event.target, "sly-order", `${order.id}:${order.get(event.target)}`);
      }
    },
    true
  );
  win.addEventListener("TabSelect", () => {
    delete win.gBrowser.selectedTab._slyUnloaded;
    applyTheme(win).catch(logError);
    win._slyLand = null;
    markLanding(win);
    const group = groupOfTab(win.gBrowser.selectedTab);
    if (group) {
      State.noteGroupUse(group.id);
    }
  });
  win.addEventListener("ZenWorkspacesUIUpdate", () => {
    const active = win.gZenWorkspaces.activeWorkspace;
    if (win._slySpace !== active) {
      win._slySpace = active;
      looseOrder.delete(active);
      groupOrder.delete(active);
    }
    applyTheme(win).catch(logError);
    renderFooter(win);
  });
  win.addEventListener(
    "keydown",
    event => {
      const digit = /^Digit([1-9])$/.exec(event.code)?.[1];
      if (!digit || !event.metaKey || event.altKey || event.ctrlKey || event.shiftKey) {
        return;
      }
      const space = win.gZenWorkspaces.getWorkspaces()[Number(digit) - 1];
      if (space) {
        event.preventDefault();
        event.stopImmediatePropagation();
        win.gZenWorkspaces.changeWorkspaceWithID(space.uuid).catch(logError);
      }
    },
    true
  );
  win.document.getElementById("cmd_close")?.addEventListener("command", event => {
    const tab = win.gBrowser.selectedTab;
    if (!tab || tab.hasAttribute("zen-essential") || (!tab.pinned && !groupOfTab(tab))) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    if (groupOfTab(tab)) {
      unloadTab(win, tab).catch(logError);
    } else {
      win.gBrowser.removeTab(tab);
    }
  });
  win.document.getElementById("History:RestoreLastClosedTabOrWindowOrSession")?.addEventListener("command", event => {
    const tab = realTabs(win)
      .filter(entry => entry._slyUnloaded && entry.hasAttribute("pending"))
      .sort((a, b) => b._slyUnloaded - a._slyUnloaded)[0];
    if (!tab || lazy.SessionStore.getClosedTabDataForWindow(win)[0]?.closedAt > tab._slyUnloaded) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    const space = tab.getAttribute("zen-workspace-id");
    Promise.resolve(win.gZenWorkspaces.activeWorkspace === space || win.gZenWorkspaces.changeWorkspaceWithID(space))
      .then(() => (win.gBrowser.selectedTab = tab))
      .catch(logError);
  });
  new win.MutationObserver(() => {
    if (win.gURLBar.hasAttribute("zen-newtab")) {
      win._slyNewTabGroup = landingGroup(win);
    }
  }).observe(win.gURLBar, { attributes: true, attributeFilter: ["zen-newtab"] });
  const noteFocus = () => recordFocus(win);
  win.addEventListener("activate", () => win.setTimeout(() => sampleSidebar(win).catch(logError), 400));
  for (const event of ["TabSelect", "TabAttrModified", "activate", "deactivate", "visibilitychange"]) {
    win.addEventListener(event, noteFocus);
  }
  win.setInterval(() => recordFocus(win, true), 60000);
  win.setInterval(() => syncLive(win, null), 60000);
  win.addEventListener("TabSelect", () => syncLive(win, win.gBrowser.selectedTab));
  win.gBrowser.addTabsProgressListener({
    onLocationChange(browser, webProgress) {
      if (webProgress.isTopLevel) {
        const moved = win.gBrowser.getTabForBrowser(browser);
        const address = browser.currentURI?.spec ?? "";
        if (moved?._slyOpener && /^https?:/.test(address)) {
          const opener = moved._slyOpener;
          delete moved._slyOpener;
          const from = groupOfTab(opener);
          if (from && !moved.hasAttribute("sly-page") && !moved.hasAttribute("sly-group")) {
            tagTab(moved, "sly-group", from.id);
          } else if (!from) {
            groupLoose(win, opener, moved).catch(logError);
          }
        }
        if (moved && !moved.hasAttribute("sly-page") && /^https?:/.test(address)) {
          const member = moved.getAttribute("sly-group");
          const group = member ? State.groups.find(entry => entry.id === member) : groupOfPage(address, moved.getAttribute("zen-workspace-id"));
          if (group?.sections.some(section => section.pages.some(page => page.url === address))) {
            if (pageTaken(win, moved, address, group.space)) {
              tagTab(moved, "sly-group", group.id);
            } else {
              tagTab(moved, "sly-page", address);
            }
          }
        }
        if (browser === win.gBrowser.selectedBrowser) {
          recordFocus(win);
        }
        if (browser === win.gBrowser.selectedBrowser) {
          applyTheme(win).catch(logError);
        }
        schedule();
      }
    },
  });
  takeSearchKey(win);
  takeSpaceIconMenu(win);
  takeSpaceMenu(win);
  routeExternal(win);
  const putProfiles = () => {
    for (const space of win.gZenWorkspaces.getWorkspaces()) {
      putSpaceLook(win, space);
    }
  };
  win.addEventListener("ZenWorkspacesUIUpdate", () => {
    try {
      putProfiles();
    } catch (error) {
      logError(error);
    }
  });
  putProfiles();
  schedule();
}

function readPref(name) {
  return JSON.parse(Services.prefs.getStringPref(name, "{}"));
}

function writePref(name, value) {
  Services.prefs.setStringPref(name, JSON.stringify(value));
}

function spaceMode(space) {
  return readPref(modesPref)[space] ?? "groups";
}

function element(doc, tag, className, attributes = {}) {
  const node = doc.createXULElement(tag);
  if (className) {
    node.className = className;
  }
  for (const [name, value] of Object.entries(attributes)) {
    node.setAttribute(name, value);
  }
  return node;
}

function stopEvent(event) {
  event.stopPropagation();
}

function spaceButton(doc, className, tooltip) {
  const button = element(doc, "toolbarbutton", `toolbarbutton-1 chromeclass-toolbar-additional sly-action ${className}`, { tooltiptext: tooltip });
  button.dataset.tooltip = tooltip;
  for (const event of ["click", "dblclick"]) {
    button.addEventListener(event, stopEvent);
  }
  return button;
}

function radioItems(doc, name, options, current, choose) {
  return options.map(([value, label]) => {
    const item = element(doc, "menuitem", "", { type: "radio", name, label });
    if (value === current) {
      item.setAttribute("checked", "true");
    }
    item.addEventListener("command", () => choose(value));
    return item;
  });
}

function menuButton(button, fill) {
  const menu = button.ownerDocument.createXULElement("menupopup");
  menu.addEventListener("popupshowing", () => {
    menu.replaceChildren(...fill());
    iconize(menu);
  });
  button.setAttribute("type", "menu");
  button.append(menu);
  return button;
}

function decorateSpaces(win) {
  const doc = win.document;
  let added = false;
  for (const indicator of doc.querySelectorAll(".zen-current-workspace-indicator")) {
    const workspace = indicator.closest("zen-workspace");
    const space = workspace?.id;
    const actions = indicator.querySelector(":scope > .zen-workspaces-actions");
    if (!space || !actions || indicator.querySelector(":scope > .sly-action")) {
      continue;
    }
    const loose = element(doc, "vbox", "sly-open");
    dropTarget(win, loose, null, "");
    loose.addEventListener("click", () => {
      win._slyLand = "";
      markLanding(win);
    });
    indicator.before(loose);
    const board = element(doc, "vbox", "sly-cards", { "sly-loading": "true" });
    board.addEventListener("click", event => {
      win._slyLand = event.target.closest?.(".sly-card")?.dataset.key?.match(/^group:(.+)$/)?.[1] ?? null;
      markLanding(win);
    });
    workspace.querySelector(":scope > .workspace-arrowscrollbox")?.after(board);
    indicator.addEventListener(
      "click",
      event => {
        if (!workspace.hasAttribute("sly-cards") || event.target.closest(".sly-action, .zen-workspaces-actions")) {
          return;
        }
        event.stopPropagation();
        const collapsed = !workspace.hasAttribute("sly-board-collapsed");
        workspace.toggleAttribute("sly-board-collapsed", collapsed);
        animateBody(board, !collapsed);
        putCardState(`space:${space}`, { collapsed });
      },
      true
    );
    if (cardState(`space:${space}`).collapsed) {
      workspace.setAttribute("sly-board-collapsed", "true");
    }
    const foldAll = spaceButton(doc, "sly-fold-all", "Expand or collapse every group of this space");
    foldAll.addEventListener("command", () => {
      const keys = [...workspace.querySelectorAll(":scope > .sly-cards .sly-card[data-key]:not([plain])")].map(node => node.dataset.key);
      const open = keys.some(key => !opened.has(key));
      for (const key of keys) {
        if (open) {
          opened.add(key);
        } else {
          opened.delete(key);
        }
      }
      renderCards(win);
    });
    const chevron = element(doc, "image", "sly-space-chevron sly-board-chevron", { tooltiptext: "Collapse or expand this space" });
    actions.after(foldAll, chevron);
    const spaceIcon = element(doc, "hbox", "sly-space-icon", { tooltiptext: "Change the icon of this space", align: "center", pack: "center" });
    spaceIcon.addEventListener("click", event => {
      stopEvent(event);
      openSpacePicker(win, workspace, spaceIcon);
    });
    indicator.querySelector(":scope > .zen-current-workspace-indicator-stack")?.append(spaceIcon);
    applyMode(workspace, spaceMode(space));
    added = true;
  }
  return added;
}

function applyMode(workspace, mode) {
  workspace.setAttribute("sly-cards", "true");
  workspace.setAttribute("sly-mode", mode);
}

function tagTabs(win) {
  for (const tab of realTabs(win)) {
    const owner = lazy.SessionStore.getCustomTabValue(tab, "sly-page");
    if (owner && !tab.hasAttribute("sly-page")) {
      tab.setAttribute("sly-page", owner);
    }
    const member = lazy.SessionStore.getCustomTabValue(tab, "sly-group");
    if (member && !tab.hasAttribute("sly-group")) {
      tab.setAttribute("sly-group", member);
    }
    const project = State.projectOf(tabUrl(tab));
    if (project) {
      tab.setAttribute("sly-project", project);
    } else {
      tab.removeAttribute("sly-project");
    }
  }
}

async function getStats(urls) {
  const stats = new Map();
  const list = [...new Set(urls)].filter(url => /^https?:/.test(url ?? ""));
  if (list.length === 0) {
    return stats;
  }
  const db = await lazy.PlacesUtils.promiseDBConnection();
  for (let start = 0; start < list.length; start += 400) {
    const chunk = list.slice(start, start + 400);
    const params = Object.fromEntries(chunk.map((url, index) => [`u${index}`, url]));
    const rows = await db.execute(
      `SELECT url, title, visit_count, last_visit_date FROM moz_places WHERE url IN (${chunk.map((_, index) => `:u${index}`).join(",")})`,
      params
    );
    for (const row of rows) {
      stats.set(row.getResultByName("url"), {
        title: row.getResultByName("title") ?? "",
        visits: row.getResultByName("visit_count") ?? 0,
        last: (row.getResultByName("last_visit_date") ?? 0) / 1000,
      });
    }
  }
  return stats;
}

async function getHumanActivity(project) {
  if (!humanCache.loaded) {
    humanCache.loaded = (async () => {
      const saved = (await IOUtils.exists(paths.activity)) ? await IOUtils.readJSON(paths.activity) : null;
      if (saved?.version === 2 && saved.synced_at === State.projectsSyncedAt) {
        for (const [id, activity] of Object.entries(saved.projects ?? {})) {
          humanCache.set(id, activity);
        }
      }
      humanCache.synced = saved?.version === 2 && saved.synced_at === State.projectsSyncedAt ? State.projectsSyncedAt : "";
    })();
  }
  await humanCache.loaded;
  if (humanCache.synced !== State.projectsSyncedAt) {
    humanCache.clear();
    humanCache.synced = State.projectsSyncedAt;
  }
  const cached = humanCache.get(project.id);
  if (cached) {
    return cached;
  }
  const db = await lazy.PlacesUtils.promiseDBConnection();
  const prefixes = project.prefixes.slice(0, 30);
  const pages = [];
  if (prefixes.length) {
    const params = Object.fromEntries(prefixes.map((prefix, index) => [`p${index}`, `${prefix.replace(/[\\%_]/g, match => "\\" + match)}%`]));
    const rows = await db.execute(
      `SELECT url, title FROM moz_places
       WHERE last_visit_date IS NOT NULL AND hidden = 0 AND (${prefixes.map((_, index) => `lower(url) LIKE :p${index} ESCAPE '\\'`).join(" OR ")})
       ORDER BY last_visit_date DESC LIMIT 40`,
      params
    );
    for (const row of rows) {
      pages.push({ url: row.getResultByName("url"), title: row.getResultByName("title") ?? "" });
    }
  }
  const terms = project.terms ?? [];
  const searches = [];
  if (terms.length) {
    const rows = await db.execute(
      `SELECT url, last_visit_date, visit_count FROM moz_places WHERE last_visit_date IS NOT NULL AND (${searchEngines.map((_, index) => `url LIKE :s${index}`).join(" OR ")})
       ORDER BY last_visit_date DESC LIMIT 2000`,
      Object.fromEntries(searchEngines.map((pattern, index) => [`s${index}`, pattern]))
    );
    const seen = new Set();
    for (const row of rows) {
      const query = URL.parse(row.getResultByName("url"))?.searchParams.get("q");
      const lower = query?.toLowerCase();
      if (!lower || seen.has(lower) || !terms.some(term => lower.includes(term))) {
        continue;
      }
      seen.add(lower);
      searches.push({ search: query, url: row.getResultByName("url"), date: row.getResultByName("last_visit_date") / 1000, used: row.getResultByName("visit_count") });
      if (searches.length >= 20) {
        break;
      }
    }
  }
  const activity = { pages, searches };
  humanCache.set(project.id, activity);
  lazy.clearTimeout(humanCache.saving);
  humanCache.saving = lazy.setTimeout(() => {
    IOUtils.writeJSON(paths.activity, { version: 2, synced_at: humanCache.synced, projects: Object.fromEntries(humanCache) }).catch(logError);
  }, 1500);
  return activity;
}

function cardState(key) {
  return readPref(cardsPref)[key] ?? {};
}

function putCardState(key, change) {
  const states = readPref(cardsPref);
  states[key] = { ...(states[key] ?? {}), ...change };
  writePref(cardsPref, states);
}

function tabPage(tab) {
  return { url: tabUrl(tab), title: tab.label, tab, date: tab.lastAccessed };
}

function groupCards(space, tabs) {
  const cards = [];
  const held = new Set();
  for (const group of State.groups.filter(entry => entry.space === space)) {
    group.sections.forEach(section => section.pages.forEach(page => held.add(page.url)));
    cards.push(groupCard(group, tabs.filter(tab => tab.getAttribute("sly-group") === group.id)));
  }
  const members = new Set(State.groups.filter(group => group.space === space).map(group => group.id));
  const onApps = new Set(pinnedApps(space).map(app => app.url));
  const loose = tabs.filter(tab => !onApps.has(tabUrl(tab)) && !onApps.has(tab.getAttribute("sly-page")) && !members.has(tab.getAttribute("sly-group")) && !held.has(tabUrl(tab)) && !held.has(tab.getAttribute("sly-page")) && /^https?:/.test(tabUrl(tab)));
  if (loose.length) {
    let order = looseOrder.get(space);
    if (!order) {
      order = new WeakMap([...loose].sort((a, b) => b.lastAccessed - a.lastAccessed).map((tab, index) => [tab, index]));
      order.next = 0;
      order.id = Math.random().toString(16).slice(2, 14);
      looseOrder.set(space, order);
    }
    for (const tab of loose) {
      if (!order.has(tab)) {
        const [id, at] = lazy.SessionStore.getCustomTabValue(tab, "sly-order").split(":");
        order.set(tab, id === order.id ? Number(at) : --order.next);
      }
    }
    cards.unshift({
      key: `open:${space}`,
      shelf: "",
      title: "Ungrouped",
      parts: [{ label: "", kind: "section", pages: [...loose].sort((a, b) => order.get(a) - order.get(b)).map(tabPage) }],
      sort: "relevance",
      collapsed: false,
      closable: true,
      plain: true,
    });
  }
  return cards;
}

function groupCard(group, open = []) {
  const parts = group.sections.map(section => ({
    label: section.label,
    kind: section.kind,
    pages: section.pages.filter(page => page.verified).map(page => ({ ...page, section: section.label })),
  }));
  const unverified = group.sections.flatMap(section =>
    section.pages.filter(page => !page.verified).map(page => ({ ...page, section: section.label, unverified: true }))
  );
  if (unverified.length) {
    parts.push({ label: "Unverified", kind: "section", pages: unverified });
  }
  if (open.length) {
    parts.unshift({ label: "", kind: "section", pages: open.map(tab => ({ ...tabPage(tab), closable: true })) });
  }
  return { key: `group:${group.id}`, group, shelf: group.shelf ?? "", title: group.title, parts, sort: group.sort ?? "relevance", collapsed: !opened.has(`group:${group.id}`) };
}

async function projectCards(space, fallback, tabs) {
  const cards = [];
  for (const project of State.projects.filter(entry => (entry.space || fallback) === space)) {
    const linked = State.groups.find(group => group.project === project.id);
    if (linked) {
      cards.push({ ...groupCard(linked), shelf: project.shelf ?? "", project: project.id, open: 1 });
      continue;
    }
    const open = tabs.filter(tab => tab.getAttribute("sly-project") === project.id).map(tabPage);
    const human = await getHumanActivity(project);
    if (open.length === 0 && human.pages.length === 0 && human.searches.length === 0) {
      continue;
    }
    const claude = [
      ...(project.claude_pages ?? []).map(page => ({ url: page.url, title: "", used: page.sessions, date: Date.parse(page.last_seen), claude: true })),
    ];
    const state = cardState(`project:${project.id}`);
    cards.push({
      key: `project:${project.id}`,
      project: project.id,
      remote: originOf(project.remote),
      shelf: project.shelf ?? "",
      title: project.name,
      parts: [
        { label: "Open", kind: "section", pages: open },
        { label: "Assistant", kind: "section", pages: claude },
        { label: "You", kind: "section", pages: [...human.pages, ...human.searches] },
      ].filter(part => part.pages.length),
      sort: state.sort ?? "relevance",
      collapsed: !opened.has(`project:${project.id}`),
      open: open.length,
    });
  }
  const rank = shelf => (shelf === "Other" ? "\uffff" : shelf);
  return cards.sort((a, b) => rank(a.shelf).localeCompare(rank(b.shelf)) || b.open - a.open);
}

function sortPages(pages, sort, stats) {
  const when = page => page.date ?? stats.get(page.url)?.last ?? (page.added_at ? Date.parse(page.added_at) : 0);
  const used = page => page.used ?? stats.get(page.url)?.visits ?? 0;
  if (sort === "date") {
    return [...pages].sort((a, b) => when(b) - when(a));
  }
  if (sort === "used") {
    return [...pages].sort((a, b) => used(b) - used(a));
  }
  return pages;
}

async function renderCards(win) {
  if (win._slyCardsBusy) {
    win._slyCardsAgain = true;
    return;
  }
  win._slyCardsBusy = true;
  try {
    await loadIcons();
    win.document.getElementById("navigator-toolbox")?.toggleAttribute("sly-loading", !!win.document.querySelector(".sly-cards[sly-loading]"));
    for (const workspace of win.document.querySelectorAll("zen-workspace[sly-cards]")) {
      const board = workspace.querySelector(":scope > .sly-cards");
      if (!board) {
        continue;
      }
      const space = workspace.id;
      const tabs = [...win.gZenWorkspaces.allStoredTabs].filter(tab => tab.getAttribute("zen-workspace-id") === space && !tab.hasAttribute("zen-essential") && !tab.hasAttribute("zen-glance-tab") && (!tab.hasAttribute("zen-empty-tab") || tab.hasAttribute("sly-group")));
      if (workspace.getAttribute("sly-mode") === "projects" && humanCache.synced !== State.projectsSyncedAt) {
        board.setAttribute("sly-loading", "true");
        win.document.getElementById("navigator-toolbox")?.setAttribute("sly-loading", "true");
      }
      const cards = workspace.getAttribute("sly-mode") === "projects" ? await projectCards(space, win.gZenWorkspaces.getWorkspaces()[0]?.uuid, tabs) : groupCards(space, tabs);
      const urls = cards.flatMap(card => card.parts.flatMap(part => part.pages.map(page => page.url)));
      const stats = await getStats(urls);
      const openByUrl = new Map();
      for (const tab of tabs.filter(tab => !tab.hasAttribute("sly-group"))) {
        for (const key of [tabUrl(tab), tab.getAttribute("sly-page")]) {
          if (key && !openByUrl.get(key)?.selected) {
            openByUrl.set(key, tab);
          }
        }
      }
      learnIcons(tabs);
      assignIcons(cards, stats, openByUrl);
      for (const card of cards) {
        if (card.group) {
          groupIcons.set(card.group.id, card.icon);
        }
      }
      paintSpaceIcon(workspace, cards);
      const render = card => renderCard(win, space, card, stats, openByUrl);
      workspace.querySelector(":scope > .sly-open")?.replaceChildren(...cards.filter(card => card.plain).map(render));
      const listed = workspace.getAttribute("sly-mode") === "projects" ? shelved(win, cards.filter(card => !card.plain), render) : spaceBlocks(win, workspace, cards, render);
      board.replaceChildren(...listed, ...aiArea(win, space));
      board.removeAttribute("sly-loading");
      board.style.maxHeight = workspace.hasAttribute("sly-board-collapsed") ? "0px" : "none";
    }
  } catch (error) {
    logError(error);
  } finally {
    win._slyCardsBusy = false;
    markLanding(win);
    resolveIcons(win).catch(logError);
    applyTheme(win).catch(logError);
    if (win._slyCardsAgain) {
      win._slyCardsAgain = false;
      win._slySchedule?.();
    }
  }
}

function pageIcon(page, tab) {
  return tab?.getAttribute("image") || siteIcon(page.url);
}

const defaultIcon = "chrome://global/skin/icons/defaultFavicon.svg";
const siteIcons = new Map();
const iconQueue = new Set();
let iconsLearned = false;
let iconsLoaded = null;

function siteIcon(url) {
  const origin = URL.parse(url)?.origin;
  if (!origin || origin === "null") {
    return defaultIcon;
  }
  if (siteIcons.has(origin)) {
    return siteIcons.get(origin) || defaultIcon;
  }
  iconQueue.add(origin);
  return defaultIcon;
}

async function loadIcons() {
  iconsLoaded ??= (async () => {
    if (await IOUtils.exists(paths.icons)) {
      for (const [origin, icon] of Object.entries(await IOUtils.readJSON(paths.icons))) {
        siteIcons.set(origin, icon);
      }
    }
  })();
  return iconsLoaded;
}

function learnIcons(tabs) {
  for (const tab of tabs) {
    const origin = originOf(tabUrl(tab));
    const image = tab.getAttribute("image") ?? "";
    const icon = image.startsWith("moz-remote-image://") ? decodeURIComponent(new URL(image).searchParams.get("url") ?? "") : image;
    if (origin && icon && !siteIcons.get(origin)) {
      siteIcons.set(origin, icon);
      iconsLearned = true;
    }
  }
}

async function findIcon(win, origin) {
  try {
    const response = await win.fetch(`${origin}/`, { credentials: "omit", signal: win.AbortSignal.timeout(6000) });
    const html = await response.text();
    const doc = new win.DOMParser().parseFromString(html, "text/html");
    const links = [...doc.querySelectorAll("link[rel][href]")].filter(link => /(^|\s)(icon|apple-touch-icon)(\s|$)/i.test(link.getAttribute("rel")));
    const best = links.find(link => /apple-touch-icon/i.test(link.getAttribute("rel"))) ?? links[0];
    if (best) {
      return new URL(best.getAttribute("href"), response.url).href;
    }
    const fallback = await win.fetch(`${origin}/favicon.ico`, { credentials: "omit", signal: win.AbortSignal.timeout(6000) });
    return fallback.ok && (fallback.headers.get("content-type") ?? "").startsWith("image") ? fallback.url : "";
  } catch (error) {
    logError(`icon ${origin}: ${error}`);
    return "";
  }
}

async function resolveIcons(win) {
  await loadIcons();
  const origins = [...iconQueue].filter(origin => !siteIcons.has(origin)).slice(0, 24);
  iconQueue.clear();
  if (origins.length === 0 && !iconsLearned) {
    return;
  }
  const found = await Promise.all(origins.map(origin => findIcon(win, origin)));
  origins.forEach((origin, index) => siteIcons.set(origin, found[index]));
  iconsLearned = false;
  await IOUtils.writeJSON(paths.icons, Object.fromEntries(siteIcons), { tmpPath: `${paths.icons}.tmp` });
  win._slySchedule?.();
}

function iconImage(doc, className, src) {
  const image = element(doc, "image", className, { src });
  image.addEventListener("error", () => image.setAttribute("src", "chrome://global/skin/icons/defaultFavicon.svg"), { once: true });
  return image;
}

async function renderApps(win) {
  const doc = win.document;
  const row = doc.getElementById("sly-apps");
  const container = doc.getElementById("urlbar-container")?.getBoundingClientRect();
  const toolbox = doc.getElementById("navigator-toolbox")?.getBoundingClientRect();
  const inset = container && toolbox ? Math.round(container.left - toolbox.left) : -1;
  if (inset >= 0 && inset < toolbox.width / 5) {
    doc.documentElement.style.setProperty("--sly-inline", `${inset}px`);
  }
  if (!row) {
    return;
  }
  await loadIcons();
  const space = win.gZenWorkspaces.activeWorkspace;
  const fit = Services.prefs.getStringPref("slybrowser.apps.fit", "width");
  const size = Number(Services.prefs.getStringPref("slybrowser.apps.size", "5"));
  row.setAttribute("fit", fit);
  row.style.setProperty("--sly-app-size", `${size}em`);
  const tabs = realTabs(win);
  const essentials = tabs.filter(tab => tab.hasAttribute("zen-essential")).map(tab => ({ url: tabUrl(tab), title: tab.label, tab }));
  const pinned = pinnedApps(space).map(app => ({ ...app, pinned: true, tab: tabs.find(tab => tab.getAttribute("zen-workspace-id") === space && (tab.getAttribute("sly-page") === app.url || tabUrl(tab) === app.url)) }));
  const apps = [...essentials, ...pinned];
  row.replaceChildren();
  for (const app of apps) {
    const tile = element(doc, "vbox", "sly-app", { tooltiptext: `${app.title}\n${app.url}`, align: "center", pack: "center" });
    tile.toggleAttribute("pinned", !!app.pinned);
    if (app.pinned) {
      tile.setAttribute("draggable", "true");
      tile.addEventListener("dragstart", event => {
        heldPage = { url: app.url, title: app.title, icon: tile.querySelector(".sly-app-icon")?.getAttribute("src") ?? null };
        event.dataTransfer.setData(appType, app.url);
        event.dataTransfer.setData("text/uri-list", app.url);
        event.dataTransfer.effectAllowed = "move";
        win.setTimeout(() => tile.setAttribute("dragging", "true"), 0);
      });
      tile.addEventListener("dragend", () => endDrag(win, tile));
    }
    tile.addEventListener("contextmenu", event => {
      event.preventDefault();
      openAppMenu(win, app, event);
    });
    const group = app.pinned ? State.groups.find(entry => entry.sections.some(section => section.pages.some(page => page.url === app.url))) : null;
    if (group) {
      const custom = parseColor(group.color);
      (custom ? Promise.resolve([custom]) : faviconPalette(win, groupIcons.get(group.id))).then(palette => paintPalette(tile, palette), logError);
    }
    tile.toggleAttribute("selected", !!app.tab?.selected);
    tile.append(iconImage(doc, "sly-app-icon", app.tab?.getAttribute("image") || siteIcon(app.icon ?? app.url)));
    tile.addEventListener("click", () => (app.tab ? (win.gBrowser.selectedTab = app.tab) : postOpenPage(win, app.url, space)));
    row.append(tile);
  }
  return row;
}

function animateBody(body, open) {
  const full = body.scrollHeight;
  body.style.maxHeight = open ? "0px" : `${full}px`;
  body.getBoundingClientRect();
  body.style.maxHeight = open ? `${full}px` : "0px";
  body.addEventListener(
    "transitionend",
    () => {
      if (open) {
        body.style.maxHeight = "none";
      }
    },
    { once: true }
  );
}

function growBody(win, body, from) {
  body.style.maxHeight = `${from}px`;
  win.requestAnimationFrame(() => {
    body.style.maxHeight = `${body.scrollHeight}px`;
    body.addEventListener("transitionend", () => (body.style.maxHeight = "none"), { once: true });
  });
}

function renderCard(win, space, card, stats, openByUrl) {
  const doc = win.document;
  const node = element(doc, "vbox", "sly-card");
  node.toggleAttribute("folded", card.collapsed);
  node.toggleAttribute("plain", !!card.plain);
  node.dataset.key = card.key;
  const login = card.group?.login ? lazy.ContextualIdentityService.getPublicIdentityFromId(loginContext(card.group.login)) : null;
  if (login) {
    node.setAttribute("sly-login", card.group.login);
    node.classList.add(`identity-color-${login.color}`);
  }
  if (card.icon && !card.plain) {
    faviconPalette(win, card.icon).then(palette => paintPalette(node, palette), logError);
  }
  node.toggleAttribute("sly-ai", thinking.has(card.key));
  const head = element(doc, "hbox", "sly-card-head", { align: "center" });
  const total = card.parts.reduce((sum, part) => sum + part.pages.length, 0);
  const sortItems = () => [
    element(doc, "menuitem", "sly-menu-header", { label: "Sort by", disabled: "true" }),
    ...radioItems(doc, `sly-sort-${card.key}`, [["relevance", "Relevance"], ["date", "Date"], ["used", "Most used"]], card.sort, sort =>
      putCard(win, card, { sort })
    ),
  ];
  const pages = `${total} ${total === 1 ? "page" : "pages"}`;
  const titleLabel = element(doc, "label", "sly-card-title", { value: card.title, crop: "end", flex: "1", tooltiptext: `${card.title} · ${pages}${login ? ` · container ${card.group.login}` : ""}` });
  const countLabel = element(doc, "label", "sly-card-count", { value: String(total), tooltiptext: pages });
  const chevron = element(doc, "image", "sly-card-chevron");
  head.setAttribute("tooltiptext", card.collapsed ? `Expand ${card.title}` : `Collapse ${card.title}`);
  if (card.plain) {
    expanded.add(card.key);
  } else {
    const icon = card.favicon && !card.group?.icon ? iconImage(doc, "sly-card-icon sly-group-icon", card.favicon) : iconElement(doc, card.icon, "sly-card-icon sly-group-icon");
    if (card.group) {
      icon.setAttribute("tooltiptext", `Change the icon of ${card.title}`);
      icon.addEventListener("click", event => {
        stopEvent(event);
        openIconPicker(win, {
          title: card.title,
          origins: card.origins,
          current: card.icon,
          anchor: icon,
          choose: value => State.changeGroup(card.group.id, group => (group.icon = value)),
        });
      });
    }
    head.append(icon, titleLabel);
  }
  let fold = null;
  if (card.group) {
    fold = element(doc, "image", "sly-similar sly-card-fold", { tooltiptext: `Expand or collapse every section of ${card.title}`, role: "button" });
    fold.addEventListener("click", event => {
      stopEvent(event);
      const paths = sectionPaths(sectionTree(card.parts));
      const open = card.collapsed || paths.some(path => cardState(`${card.key}|${path}`).collapsed);
      for (const path of paths) {
        putCardState(`${card.key}|${path}`, { collapsed: !open });
      }
      if (open) {
        opened.add(card.key);
      } else {
        opened.delete(card.key);
      }
      renderCards(win);
    });
  }
  if (!card.plain) {
    const more = aiMenu(win, card.title, card.key, card.project ? ["--project", card.project] : card.group ? ["--group", card.group.id] : null, sortItems);
    head.append(countLabel, ...(fold ? [fold] : []), more, chevron);
    head.addEventListener("contextmenu", event => {
      event.preventDefault();
      stopEvent(event);
      more.querySelector("menupopup").openPopupAtScreen(event.screenX, event.screenY, true);
    });
    node.append(head);
  }

  const body = element(doc, "vbox", "sly-card-body");
  if (card.group) {
    dropTarget(win, node, card.group.id, "");
  }
  const context = { win, space, card, stats, openByUrl, showAll: expanded.has(card.key), shown: 0, hidden: 0 };
  renderTree(body, sectionTree(card.parts), 0, context);
  const shown = context.shown;
  const showAll = context.showAll;
  body.style.maxHeight = card.collapsed ? "0px" : "none";
  node.append(body);
  if (win._slyGrow?.key === card.key) {
    growBody(win, body, win._slyGrow.from);
    win._slyGrow = null;
  }
  if (!showAll && shown + context.hidden < total) {
    node.setAttribute("truncated", "true");
    const more = element(doc, "label", "sly-card-expand", { value: `Expand all · ${total - shown - context.hidden} more`, tooltiptext: `Show all ${total} pages of ${card.title}` });
    more.addEventListener("click", () => {
      expanded.add(card.key);
      win._slyGrow = { key: card.key, from: body.offsetHeight };
      renderCards(win);
    });
    node.append(more);
  }
  head.addEventListener("click", () => {
    card.collapsed = !card.collapsed;
    if (card.collapsed) {
      opened.delete(card.key);
    } else {
      opened.add(card.key);
    }
    if (!card.collapsed && body.childElementCount === 0) {
      renderCards(win);
      return;
    }
    node.toggleAttribute("folded", card.collapsed);
    head.setAttribute("tooltiptext", card.collapsed ? `Expand ${card.title}` : `Collapse ${card.title}`);
    animateBody(body, !card.collapsed);
  });
  return node;
}

function renderRow(win, space, card, page, stats, openByUrl) {
  const doc = win.document;
  const tab = page.tab ?? (page.url ? openByUrl.get(page.url) : null);
  const row = element(doc, "hbox", "sly-card-tab", { align: "center" });
  if (page.url) {
    row.dataset.url = page.url;
  }
  row._slyTab = tab;
  row.toggleAttribute("selected", !!tab?.selected);
  row.toggleAttribute("sly-open", !!tab && !tab.hasAttribute("pending"));
  row.toggleAttribute("unverified", !!page.unverified);
  const label = page.search ?? (page.title || stats.get(page.url)?.title || page.url.replace(/^https?:\/\/(www\.)?/, ""));
  row.append(
    iconImage(doc, "sly-card-icon", pageIcon(page, tab)),
    element(doc, "label", "sly-card-label", { value: label, crop: "end", flex: "1", tooltiptext: page.search ? `Search for ${page.search}\n${page.url}` : `${label}\n${page.url}${page.rule ? `\nPlaced by the rule ${page.rule_name || page.rule}` : ""}` })
  );
  const live = page.url && !page.liveChild ? State.liveItem(page.url) : null;
  if (live) {
    row.setAttribute("sly-live", "true");
    const count = liveCount(live, tab);
    if (count > 0 || live.error) {
      const badge = element(doc, "label", "sly-row-live", { value: live.error ? "!" : String(count), tooltiptext: liveTip(live, count) });
      if (live.children?.length) {
        badge.addEventListener("click", event => {
          stopEvent(event);
          liveFolded.has(live.url) ? liveFolded.delete(live.url) : liveFolded.add(live.url);
          renderCards(win);
        });
      }
      row.append(badge);
    }
  }
  if (page.url && /^https?:/.test(page.url)) {
    const more = element(doc, "image", "sly-row-more", { tooltiptext: "More for this page" });
    more.addEventListener("click", event => {
      stopEvent(event);
      openRowMenu(win, space, row, page, event);
    });
    row.append(more);
  }
  if (page.unverified || ((card.closable || page.closable) && tab)) {
    const close = element(doc, "image", "sly-card-close", { tooltiptext: page.unverified ? "Dismiss from this group" : "Close tab" });
    close.addEventListener("click", event => {
      stopEvent(event);
      if (page.unverified) {
        dismissPage(win, card, page);
      } else if (card.group) {
        unloadTab(win, tab).catch(logError);
      } else {
        win.gBrowser.removeTab(tab);
      }
    });
    row.append(close);
  }
  if (page.url) {
    row.setAttribute("draggable", "true");
    row.addEventListener("dragstart", event => {
      if (row.closest("[sly-ai]")) {
        event.preventDefault();
        return;
      }
      startDrag(win, event, row, { url: page.url, title: label, group: card.group?.id ?? null });
    });
    row.addEventListener("dragend", () => endDrag(win, row));
  }
  if (page.url && /^https?:/.test(page.url)) {
    row.addEventListener("contextmenu", event => {
      event.preventDefault();
      stopEvent(event);
      if (row.closest("[sly-ai]")) {
        return;
      }
      openRowMenu(win, space, row, page, event);
    });
  }
  row.addEventListener("click", event => {
    if (event.button !== 0) {
      return;
    }
    openRow(win, space, card, page, tab).catch(logError);
    if (page.unverified) {
      keepPage(win, card, page);
    }
  });
  return row;
}

async function openRow(win, space, card, page, tab) {
  const context = card.group?.login ? loginContext(card.group.login) : getSpaceContext(win, space);
  const opened = (tab && tab.userContextId === context ? tab : null) ?? (await postOpenPage(win, page.url, space, { userContextId: context }));
  if (opened === tab && tab.getAttribute("sly-page") === page.url && tabUrl(tab) !== page.url) {
    tab.linkedBrowser.fixupAndLoadURIString(page.url, { triggeringPrincipal: Services.scriptSecurityManager.getSystemPrincipal() });
  }
  if (card.group && !page.tab) {
    tagTab(opened, "sly-page", page.url);
  }
  win.gBrowser.selectedTab = opened;
}

async function storeCard(card, change) {
  if (card.group) {
    await State.changeGroup(card.group.id, group => Object.assign(group, change));
  } else {
    putCardState(card.key, change);
  }
}

async function putCard(win, card, change) {
  await storeCard(card, change);
  renderCards(win);
}

async function keepPage(win, card, page) {
  await State.changeGroup(card.group.id, group => {
    for (const section of group.sections) {
      for (const entry of section.pages) {
        if (entry.url === page.url) {
          entry.verified = true;
        }
      }
    }
  });
  State.learn({ action: "kept", url: page.url, title: page.title, group: card.group.title, section: page.section ?? "" });
  renderCards(win);
}

async function dismissPage(win, card, page) {
  await State.changeGroup(card.group.id, group => {
    for (const section of group.sections) {
      section.pages = section.pages.filter(entry => entry.url !== page.url);
    }
  });
  State.learn({ action: "dismissed", url: page.url, title: page.title, group: card.group.title, section: page.section ?? "" });
  renderCards(win);
}

function settle(win, button, state, message) {
  button.removeAttribute("busy");
  button.setAttribute(state, "true");
  button.setAttribute("tooltiptext", message);
  win.clearTimeout(button._slySettle);
  button._slySettle = win.setTimeout(
    () => {
      button.removeAttribute(state);
      button.setAttribute("tooltiptext", button.dataset.tooltip);
    },
    state === "failed" ? 12000 : 4000
  );
}

async function runSlyb(win, args, button, scope) {
  if (button.hasAttribute("busy")) {
    return false;
  }
  if (scope) {
    thinking.add(scope);
    paintThinking(win);
  }
  const slyb = State.settings.slyb;
  if (!slyb) {
    settle(win, button, "failed", "slyb is not set up: run slyb install");
    return false;
  }
  button.removeAttribute("done");
  button.removeAttribute("failed");
  button.setAttribute("busy", "true");
  button.setAttribute("tooltiptext", "Working…");
  try {
    const result = await runSlybCommand(args);
    settle(win, button, "done", result?.summary ?? "Done");
    return true;
  } catch (error) {
    settle(win, button, "failed", String(error.message ?? error));
    logError(error);
    return false;
  } finally {
    if (scope) {
      thinking.delete(scope);
      paintThinking(win);
    }
  }
}

function shelved(win, cards, render) {
  const order = [...new Set(cards.map(card => card.shelf))].sort((a, b) => (a === "" ? -1 : b === "" ? 1 : a.localeCompare(b)));
  const nodes = [];
  for (const shelf of order) {
    if (shelf) {
      nodes.push(element(win.document, "label", "sly-shelf", { value: shelf, crop: "end" }));
    }
    nodes.push(...cards.filter(card => card.shelf === shelf).map(render));
  }
  return nodes;
}

function renderViews(win) {
  const mode = spaceMode(win.gZenWorkspaces.activeWorkspace);
  for (const button of win.document.querySelectorAll(".sly-view")) {
    button.toggleAttribute("checked", button.id === `sly-view-${mode}`);
  }
}

function originOf(url) {
  const origin = URL.parse(url ?? "")?.origin;
  return origin && origin !== "null" ? origin : null;
}

function assignIcons(cards, stats, openByUrl) {
  for (const card of cards) {
    const weights = new Map();
    let top = null;
    for (const part of card.parts) {
      for (const page of part.pages) {
        const origin = page.search ? null : originOf(page.url);
        if (!origin) {
          continue;
        }
        const weight = 1 + (stats.get(page.url)?.visits ?? 0);
        weights.set(origin, (weights.get(origin) ?? 0) + weight);
        if (!top || weight > top.weight) {
          top = { page, origin, weight };
        }
      }
    }
    card.origins = [...weights].sort((a, b) => b[1] - a[1]).map(([origin]) => origin);
    if (!card.origins.length && card.remote) {
      card.origins = [card.remote];
    }
    card.weight = [...weights.values()].reduce((sum, weight) => sum + weight, 0);
    if (card.group?.icon) {
      card.icon = card.group.icon;
      continue;
    }
    if (top) {
      card.icon = top.origin;
      card.favicon = pageIcon(top.page, top.page.tab ?? openByUrl.get(top.page.url));
    }
  }
}

function iconSource(icon) {
  if (!icon) {
    return defaultIcon;
  }
  if (/^https?:/.test(icon)) {
    return siteIcon(`${icon}/`);
  }
  return /^(chrome|data|moz-extension|resource):/.test(icon) ? icon : null;
}

function iconElement(doc, icon, className) {
  const source = iconSource(icon);
  if (source) {
    return iconImage(doc, className, source);
  }
  return element(doc, "label", `${className} sly-emoji`, { value: icon });
}


function iconPanel(doc) {
  let panel = doc.getElementById("sly-icon-panel");
  if (!panel) {
    panel = element(doc, "panel", "sly-icon-panel", { id: "sly-icon-panel", type: "arrow", orient: "vertical" });
    doc.getElementById("mainPopupSet").append(panel);
  }
  return panel;
}


function sectionTree(parts) {
  const root = { label: "", path: "", children: new Map(), pages: [] };
  for (const part of parts) {
    let node = root;
    for (const name of (part.label ?? "").split(" / ").map(value => value.trim()).filter(Boolean)) {
      const path = node.path ? `${node.path} / ${name}` : name;
      if (!node.children.has(name)) {
        node.children.set(name, { label: name, path, children: new Map(), pages: [] });
      }
      node = node.children.get(name);
    }
    node.pages.push(...part.pages);
  }
  return root;
}

function treeCount(node) {
  return node.pages.length + [...node.children.values()].reduce((sum, child) => sum + treeCount(child), 0);
}

const liveFolded = new Set();
const liveSyncing = new Set();

function liveCount(live, tab) {
  if (live.source !== "title") {
    return live.count ?? 0;
  }
  return Number(tab?.label?.match(/\((\d+)\)/)?.[1] ?? 0);
}

function liveTip(live, count) {
  if (live.error) {
    return `Live, but the last sync failed: ${live.error}`;
  }
  const synced = live.synced_at ? `\nSynced ${new Date(live.synced_at).toLocaleTimeString()}` : "";
  const every = live.every ? `, every ${live.every} min` : ", when opened";
  if (live.source === "title") {
    return `${count} on the page, read from its tab title`;
  }
  const pulls = (live.children ?? []).filter(child => child.kind === "pr").length;
  return `${pulls} open pull ${pulls === 1 ? "request" : "requests"} and ${count - pulls} open ${count - pulls === 1 ? "issue" : "issues"}${every}${synced}${live.children?.length ? "\nClick to fold them" : ""}`;
}

function renderLiveChildren(win, space, card, page, stats, openByUrl) {
  const live = page.url ? State.liveItem(page.url) : null;
  if (!live?.children?.length || liveFolded.has(live.url)) {
    return [];
  }
  return live.children.map(child => {
    const row = renderRow(win, space, card, { url: child.url, title: `${child.kind === "pr" ? "PR" : "Issue"} #${child.number} ${child.draft ? "(draft) " : ""}${child.title}`, liveChild: true }, stats, openByUrl);
    row.classList.add("sly-live-child");
    return row;
  });
}

function syncLive(win, tab) {
  const now = Date.now();
  const age = item => now - (Date.parse(item.synced_at ?? "") || 0);
  const opened = tab ? State.liveItem(tabUrl(tab) ?? "") : null;
  const due = State.live.filter(item => item.source === "github" && !liveSyncing.has(item.url) && (tab ? opened?.url === item.url && age(item) > 30000 : item.every > 0 && age(item) >= item.every * 60000));
  for (const item of due) {
    liveSyncing.add(item.url);
    runSlybCommand(["live", "sync", item.url])
      .catch(logError)
      .finally(() => liveSyncing.delete(item.url));
  }
}

function renderTree(container, node, depth, context) {
  const { win, space, card, stats, openByUrl } = context;
  const doc = win.document;
  for (const page of sortPages(node.pages, card.sort, stats)) {
    if (!context.showAll && context.shown >= rowLimit) {
      return;
    }
    container.append(renderRow(win, space, card, page, stats, openByUrl));
    context.shown++;
    container.append(...renderLiveChildren(win, space, card, page, stats, openByUrl));
  }
  for (const child of node.children.values()) {
    if (!context.showAll && context.shown >= rowLimit) {
      return;
    }
    const count = treeCount(child);
    const key = `${card.key}|${child.path}`;
    let folded = !!cardState(key).collapsed;
    const block = element(doc, "vbox", "sly-section");
    if (card.group) {
      dropTarget(win, block, card.group.id, child.path);
    }
    block.setAttribute("depth", String(depth));
    block.toggleAttribute("folded", folded);
    const head = element(doc, "hbox", "sly-section-head", { align: "center", tooltiptext: folded ? `Expand ${child.label}` : `Collapse ${child.label}` });
    if (depth > 0) {
      head.append(element(doc, "box", "sly-section-stem"));
    }
    const sectionKey = `${key}`;
    block.dataset.key = sectionKey;
    block.toggleAttribute("sly-ai", thinking.has(sectionKey));
    const actions = [];
    if (card.group) {
      actions.push(aiMenu(win, child.label, sectionKey, ["--group", card.group.id, "--section", child.path]));
    }
    head.append(
      element(doc, "label", "sly-section-title", { value: child.label, crop: "end", flex: "1", tooltiptext: `${child.path} · ${count} ${count === 1 ? "page" : "pages"}` }),
      element(doc, "label", "sly-card-count", { value: String(count), tooltiptext: `${count} ${count === 1 ? "page" : "pages"}` }),
      ...actions,
      element(doc, "image", "sly-card-chevron")
    );
    if (actions.length) {
      head.addEventListener("contextmenu", event => {
        event.preventDefault();
        stopEvent(event);
        actions[0].querySelector("menupopup").openPopupAtScreen(event.screenX, event.screenY, true);
      });
    }
    const body = element(doc, "vbox", "sly-section-body");
    body.style.maxHeight = folded ? "0px" : "none";
    head.addEventListener("click", event => {
      stopEvent(event);
      folded = !folded;
      if (!folded && body.childElementCount === 0) {
        renderTree(body, child, depth + 1, { ...context, showAll: true, shown: 0, hidden: 0 });
      }
      block.toggleAttribute("folded", folded);
      head.setAttribute("tooltiptext", folded ? `Expand ${child.label}` : `Collapse ${child.label}`);
      animateBody(body, !folded);
      putCardState(key, { collapsed: folded });
    });
    block.append(head, body);
    container.append(block);
    if (folded) {
      context.hidden += count;
    } else {
      renderTree(body, child, depth + 1, context);
    }
  }
}

function renderPage(win) {
  const doc = win.document;
  const page = doc.getElementById("sly-page");
  if (!page) {
    return;
  }
  const tab = win.gBrowser.selectedTab;
  const url = win.gBrowser.currentURI?.spec ?? "";
  const host = url.startsWith("moz-extension:") ? "New tab" : (URL.parse(url)?.host ?? "");
  page.querySelector(".sly-page-icon").setAttribute("src", tab?.getAttribute("image") || defaultIcon);
  const text = page.querySelector(".sly-page-text");
  text.replaceChildren(
    element(doc, "label", "sly-page-title", { value: tab?.label ?? "", crop: "end" }),
    element(doc, "label", "sly-page-host", { value: host || url, crop: "center" })
  );
  page.setAttribute("tooltiptext", url);
  const key = tab?.getAttribute("sly-page") || url;
  const group = State.groups.find(entry => entry.sections.some(section => section.pages.some(item => item.url === key)));
  const custom = parseColor(group?.color);
  const look = custom ? Promise.resolve([custom]) : faviconPalette(win, (group && groupIcons.get(group.id)) || originOf(url));
  look.then(palette => {
    if ((win.gBrowser.currentURI?.spec ?? "") !== url) {
      return;
    }
    page.removeAttribute("sly-painted");
    page.removeAttribute("sly-ink");
    paintPalette(page, palette);
  }, logError);
}

function attachTips(win) {
  const doc = win.document;
  const tip = doc.createElementNS("http://www.w3.org/1999/xhtml", "div");
  tip.id = "sly-tip";
  doc.documentElement.append(tip);
  let current = null;
  const ours = node => node.closest?.(".sly-cards, .sly-open, #sly-apps, #sly-page, .sly-action, .sly-view, .sly-space-icon, .sly-board-chevron, #sly-icon-panel");
  let muted = [];
  const hide = () => {
    if (current?._slyTip !== undefined && !current.hasAttribute("tooltiptext")) {
      current.setAttribute("tooltiptext", current._slyTip);
    }
    for (const [node, text] of muted) {
      if (!node.hasAttribute("tooltiptext")) {
        node.setAttribute("tooltiptext", text);
      }
    }
    muted = [];
    current = null;
    tip.removeAttribute("shown");
  };
  doc.addEventListener("mouseover", event => {
    const target = event.target.closest?.("[tooltiptext]");
    if (!target || !ours(target)) {
      if (current && !current.contains(event.target)) {
        hide();
      }
      return;
    }
    if (target === current) {
      return;
    }
    hide();
    current = target;
    current._slyTip = target.getAttribute("tooltiptext");
    target.removeAttribute("tooltiptext");
    for (let node = target.parentElement; node; node = node.parentElement) {
      if (node.hasAttribute?.("tooltiptext")) {
        muted.push([node, node.getAttribute("tooltiptext")]);
        node.removeAttribute("tooltiptext");
      }
    }
    tip.textContent = current._slyTip;
    tip.setAttribute("shown", "true");
    const rect = target.getBoundingClientRect();
    const width = tip.offsetWidth;
    const left = Math.max(6, Math.min(rect.left + rect.width / 2 - width / 2, win.innerWidth - width - 6));
    const below = rect.bottom + 6 + tip.offsetHeight < win.innerHeight;
    tip.style.left = `${left}px`;
    tip.style.top = `${below ? rect.bottom + 6 : rect.top - tip.offsetHeight - 6}px`;
  });
  doc.addEventListener("mouseout", event => {
    if (current && !current.contains(event.relatedTarget)) {
      hide();
    }
  });
  doc.addEventListener("mousedown", hide, true);
}

function paintThinking(win) {
  for (const workspace of win.document.querySelectorAll("zen-workspace")) {
    workspace.toggleAttribute("sly-ai", thinking.has(`space:${workspace.id}`));
  }
  for (const node of win.document.querySelectorAll(".sly-card[data-key], .sly-section[data-key], .sly-space-block[data-key]")) {
    node.toggleAttribute("sly-ai", thinking.has(node.dataset.key));
  }
}

function paintSpaceIcon(workspace, cards) {
  const holder = workspace.querySelector(".sly-space-icon");
  if (!holder) {
    return;
  }
  const weights = new Map();
  for (const card of cards.filter(entry => !entry.plain)) {
    card.origins.forEach((origin, index) => weights.set(origin, (weights.get(origin) ?? 0) + card.weight / (index + 1)));
  }
  workspace._slyOrigins = [...weights].sort((a, b) => b[1] - a[1]).map(([origin]) => origin);
  const top = cards.filter(card => !card.plain && card.icon).sort((a, b) => b.weight - a.weight)[0];
  workspace._slyIcon = top?.icon ?? workspace._slyOrigins[0];
  const icon = readPref(spaceIconsPref)[workspace.id] ?? top?.favicon ?? workspace._slyIcon;
  holder.replaceChildren(top?.favicon && icon === top.favicon ? iconImage(workspace.ownerDocument, "sly-space-icon-image", icon) : iconElement(workspace.ownerDocument, icon, "sly-space-icon-image"));
}

async function openIconPicker(win, { title, origins, current, anchor, choose }) {
  const doc = win.document;
  const panel = iconPanel(doc);
  const pick = async icon => {
    panel.hidePopup();
    await choose(icon);
    renderCards(win);
  };
  const grid = element(doc, "hbox", "sly-icon-grid");
  for (const origin of origins.slice(0, 16)) {
    const tile = element(doc, "vbox", "sly-icon-tile", { tooltiptext: origin.replace(/^https?:\/\/(www\.)?/, ""), align: "center", pack: "center" });
    tile.toggleAttribute("selected", origin === current);
    tile.append(iconImage(doc, "sly-icon-tile-image", siteIcon(`${origin}/`)));
    tile.addEventListener("click", () => pick(origin));
    grid.append(tile);
  }
  const footer = element(doc, "hbox", "sly-icon-footer");
  const emoji = element(doc, "button", "sly-icon-button", { label: "Emoji or symbol…", tooltiptext: "Pick an emoji or a symbol" });
  emoji.addEventListener("command", () => {
    panel.hidePopup();
    win.gZenEmojiPicker.open(anchor, { closeOnSelect: true, allowNone: false, onSelect: icon => pick(icon) });
  });
  const automatic = element(doc, "button", "sly-icon-button", { label: "Automatic", tooltiptext: "Follow the most visited site" });
  automatic.addEventListener("command", () => pick(""));
  footer.append(emoji, automatic);
  panel.replaceChildren(
    element(doc, "label", "sly-icon-title", { value: title }),
    Object.assign(element(doc, "description", "sly-icon-hint"), { textContent: "Pick one of its sites, an emoji, or let it follow the most visited site" }),
    grid,
    footer
  );
  panel.openPopup(anchor, "after_start");
}

function sectionPaths(node) {
  return [...node.children.values()].flatMap(child => [child.path, ...sectionPaths(child)]);
}

const dragType = "application/x-sly-page";
const appType = "application/x-sly-app";
let heldPage = null;

function placeAppHolder(win, row, event) {
  const doc = win.document;
  let holder = row.querySelector(".sly-app[placeholder]");
  if (!holder) {
    holder = element(doc, "vbox", "sly-app", { placeholder: "true", align: "center", pack: "center" });
    holder.append(iconImage(doc, "sly-app-icon", heldPage.icon || siteIcon(heldPage.url)));
  }
  const tiles = [...row.children].filter(tile => tile !== holder && !tile.hasAttribute("dragging"));
  const pinned = tiles.filter(tile => tile.hasAttribute("pinned"));
  const before = pinned.find(tile => {
    const box = tile.getBoundingClientRect();
    return event.clientY < box.top || (event.clientY < box.bottom && event.clientX < box.left + box.width / 2);
  });
  if (before) {
    if (holder.nextSibling !== before) {
      before.before(holder);
    }
  } else if (row.lastElementChild !== holder) {
    row.append(holder);
  }
}

function startDrag(win, event, row, page) {
  const doc = win.document;
  heldPage = { ...page, icon: row.querySelector(".sly-card-icon")?.getAttribute("src") ?? null, tab: row._slyTab ?? null };
  event.dataTransfer.setData(dragType, JSON.stringify(page));
  event.dataTransfer.setData("text/uri-list", page.url);
  event.dataTransfer.setData("text/plain", page.url);
  event.dataTransfer.effectAllowed = "copyMove";
  const ghost = doc.createElementNS("http://www.w3.org/1999/xhtml", "div");
  ghost.id = "sly-drag";
  const icon = doc.createElementNS("http://www.w3.org/1999/xhtml", "img");
  icon.src = row.querySelector(".sly-card-icon")?.getAttribute("src") ?? defaultIcon;
  const text = doc.createElementNS("http://www.w3.org/1999/xhtml", "span");
  text.textContent = page.title;
  ghost.append(icon, text);
  doc.documentElement.append(ghost);
  event.dataTransfer.setDragImage(ghost, 14, 14);
  win.setTimeout(() => ghost.remove(), 0);
  row.setAttribute("dragging", "true");
  doc.documentElement.setAttribute("sly-dragging", "true");
}

function endDrag(win, row) {
  heldPage = null;
  row.removeAttribute("dragging");
  win.document.documentElement.removeAttribute("sly-dragging");
  for (const target of win.document.querySelectorAll("[sly-drop]")) {
    target.removeAttribute("sly-drop");
  }
  win.document.querySelector("#sly-apps > .sly-app[placeholder]")?.remove();
}

function dropTarget(win, node, groupId, path) {
  node.addEventListener("dragover", event => {
    if (!event.dataTransfer.types.includes(dragType)) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    event.dataTransfer.dropEffect = "move";
    for (const other of win.document.querySelectorAll("[sly-drop]")) {
      if (other !== node) {
        other.removeAttribute("sly-drop");
      }
    }
    node.setAttribute("sly-drop", "true");
  });
  node.addEventListener("dragleave", event => {
    if (!node.contains(event.relatedTarget)) {
      node.removeAttribute("sly-drop");
    }
  });
  node.addEventListener("drop", event => {
    if (!event.dataTransfer.types.includes(dragType)) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    node.removeAttribute("sly-drop");
    movePage(win, JSON.parse(event.dataTransfer.getData(dragType)), groupId, path, heldPage?.tab ?? null).catch(logError);
  });
}

async function movePage(win, page, groupId, path, tab) {
  await State.loadGroups();
  const source = page.group ? State.groups.find(group => group.id === page.group) : null;
  const target = groupId ? State.groups.find(group => group.id === groupId) : null;
  const member = tab?.getAttribute("sly-group") ?? null;
  if (!source && !target && !member) {
    return;
  }
  let moved = null;
  if (source) {
    for (const section of source.sections) {
      const index = section.pages.findIndex(entry => entry.url === page.url);
      if (index >= 0) {
        if (source === target && section.label === path) {
          return;
        }
        moved = section.pages.splice(index, 1)[0];
      }
    }
  }
  if (target && member && !moved && !path) {
    if (member === target.id) {
      return;
    }
    tagTab(tab, "sly-group", target.id);
  } else if (target) {
    let section = target.sections.find(entry => entry.label === path);
    if (!section) {
      section = { id: Math.random().toString(16).slice(2, 14), label: path, kind: "section", pages: [] };
      target.sections.push(section);
    }
    section.pages = section.pages.filter(entry => entry.url !== page.url);
    section.pages.push({
      ...moved,
      url: page.url,
      title: moved?.title || page.title,
      source: moved?.source ?? "you",
      verified: true,
      added_at: moved?.added_at ?? new Date().toISOString(),
    });
    if (tab) {
      tagTab(tab, "sly-page", page.url);
    }
  } else if (tab) {
    tagTab(tab, null);
  }
  if (target) {
    opened.add(`group:${target.id}`);
  }
  await State.saveGroups();
  if (!target && !tab && moved) {
    await postOpenPage(win, page.url, source.space);
  }
  State.learn({ action: target ? "kept" : "dismissed", url: page.url, title: page.title, group: target?.title ?? source?.title ?? "", section: path });
  renderCards(win);
}

function openSpacePicker(win, workspace, anchor) {
  const indicator = workspace.querySelector(".zen-current-workspace-indicator");
  openIconPicker(win, {
    title: win.gZenWorkspaces.getWorkspaceFromId(workspace.id)?.name ?? "Workspace",
    origins: workspace._slyOrigins ?? [],
    current: readPref(spaceIconsPref)[workspace.id] ?? workspace._slyIcon,
    anchor,
    choose: icon => {
      const icons = readPref(spaceIconsPref);
      if (icon) {
        icons[workspace.id] = icon;
      } else {
        delete icons[workspace.id];
      }
      writePref(spaceIconsPref, icons);
    },
  });
}

const searchDay = new Intl.DateTimeFormat("en-GB", { weekday: "long", day: "numeric", month: "long" });
const searchTime = new Intl.DateTimeFormat("en-GB", { hour: "2-digit", minute: "2-digit" });

function takeSearchKey(win) {
  win.addEventListener(
    "keydown",
    event => {
      if (event.code === "KeyK" && event.metaKey !== event.ctrlKey && !event.altKey && !event.shiftKey) {
        event.preventDefault();
        event.stopImmediatePropagation();
        openSearchDialog(win);
      }
    },
    true
  );
}

function openSearchDialog(win) {
  const doc = win.document;
  let panel = doc.getElementById("sly-search");
  if (!panel) {
    panel = element(doc, "panel", "sly-search", { id: "sly-search", orient: "vertical", noautofocus: "true" });
    doc.getElementById("mainPopupSet").append(panel);
  }
  if (panel.state === "open" || panel.state === "showing") {
    panel.querySelector(".sly-search-field")?.select();
    return;
  }
  const html = (name, className, text = "") => {
    const node = doc.createElementNS("http://www.w3.org/1999/xhtml", name);
    node.className = className;
    node.textContent = text;
    return node;
  };
  const field = html("input", "sly-search-field");
  field.placeholder = "Search bookmarks, tabs and history";
  field.spellcheck = false;
  const esc = html("button", "sly-search-esc", "Esc");
  esc.addEventListener("click", () => panel.hidePopup());
  const chip = html("span", "sly-search-scope");
  chip.hidden = true;
  const head = html("div", "sly-search-head");
  head.append(html("span", "sly-search-head-icon"), chip, field, esc);
  let scope = null;
  const putScope = next => {
    scope = next;
    chip.hidden = !scope;
    field.placeholder = scope ? `Search in ${scope.label}` : "Search bookmarks, tabs and history";
    if (scope) {
      const remove = html("button", "sly-search-scope-remove", "×");
      remove.title = "Remove filter";
      remove.addEventListener("click", () => putScope(null));
      chip.title = `${scope.kind === "space" ? "Space" : scope.kind === "group" ? "Group" : "Folder"} ${scope.label}`;
      chip.replaceChildren(html("span", "sly-search-scope-label", scope.label), remove);
    }
    field.focus();
    update().catch(logError);
  };
  const list = html("div", "sly-search-list");
  const foot = html("div", "sly-search-foot");
  for (const [keys, label] of [[["↑", "↓"], "navigate"], [["↵"], "select"], [["Esc"], "close"]]) {
    const entry = html("span", "sly-search-keys");
    entry.append(...keys.map(key => html("kbd", "", key)), label);
    foot.append(entry);
  }
  const box = html("div", "sly-search-box");
  box.append(head, list, foot);
  panel.replaceChildren(box);
  let rows = [];
  let active = 0;
  let run = 0;
  const highlight = index => {
    rows[active]?.node.classList.remove("sly-search-item-active");
    active = index;
    rows[active]?.node.classList.add("sly-search-item-active");
    rows[active]?.node.scrollIntoView({ block: "nearest" });
  };
  const pick = (row, event) => {
    panel.hidePopup();
    pickSearchRow(win, row, event);
  };
  const render = (groups, terms) => {
    rows = [];
    list.replaceChildren();
    for (const group of groups) {
      list.append(html("div", "sly-search-group", group.label));
      for (const row of group.rows) {
        const index = rows.length;
        const node = html("div", "sly-search-item");
        const tile = html("span", "sly-search-item-tile");
        const icon = html("img", "sly-search-item-icon");
        icon.src = row.icon;
        icon.addEventListener("error", () => (icon.src = defaultIcon), { once: true });
        tile.append(icon);
        getIconInk(win, row.icon).then(ink => tile.setAttribute("sly-ink", ink), logError);
        const body = html("span", "sly-search-item-body");
        body.append(
          markedText(doc, "sly-search-item-title", row.title || row.url, terms),
          markedText(doc, "sly-search-item-description", row.url.replace(/^https?:\/\/(www\.)?/, ""), terms)
        );
        node.append(tile, body);
        if (row.crumbs?.length) {
          const path = html("span", "sly-search-item-path");
          path.title = row.path;
          for (const [position, crumb] of row.crumbs.entries()) {
            if (position) {
              path.append(html("span", "sly-search-crumb-slash", "/"));
            }
            const button = html("button", "sly-search-crumb", crumb.label);
            button.title = `Only ${crumb.label}`;
            button.toggleAttribute("sly-current", scope?.key === crumb.scope.key);
            button.addEventListener("click", event => {
              event.stopPropagation();
              putScope(crumb.scope);
            });
            path.append(button);
          }
          node.append(path);
        } else if (row.time) {
          node.append(html("span", "sly-search-item-time", row.time));
        }
        node.addEventListener("mouseenter", () => highlight(index));
        node.addEventListener("click", event => pick(row, event));
        rows.push({ ...row, node });
        list.append(node);
      }
    }
    if (!rows.length) {
      list.append(html("div", "sly-search-state", terms.length || scope ? "No matches" : "Start typing to search"));
    }
    active = 0;
    highlight(0);
  };
  const update = async () => {
    const id = ++run;
    const terms = field.value.toLowerCase().split(/\s+/).filter(Boolean);
    const groups = terms.length || scope ? await getSearchGroups(win, terms, scope) : [];
    if (id === run) {
      render(groups, terms);
    }
  };
  field.addEventListener("input", () => update().catch(logError));
  field.addEventListener("keydown", event => {
    if (event.key === "Backspace" && scope && field.selectionStart === 0 && field.selectionEnd === 0) {
      event.preventDefault();
      putScope(null);
    } else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (rows.length) {
        highlight((active + (event.key === "ArrowDown" ? 1 : rows.length - 1)) % rows.length);
      }
    } else if (event.key === "Enter") {
      event.preventDefault();
      if (rows[active]) {
        pick(rows[active], event);
      }
    }
  });
  render([], []);
  const width = parseFloat(win.getComputedStyle(box).width) || 0;
  panel.addEventListener("popupshown", () => field.focus(), { once: true });
  panel.openPopupAtScreen(Math.round(win.mozInnerScreenX + (win.innerWidth - width) / 2), Math.round(win.mozInnerScreenY + win.innerHeight / 6), false);
}

const iconInks = new Map();

function getIconInk(win, source) {
  if (!iconInks.has(source)) {
    iconInks.set(
      source,
      (async () => {
        const image = new win.Image();
        image.src = source;
        await image.decode();
        const canvas = win.document.createElementNS("http://www.w3.org/1999/xhtml", "canvas");
        canvas.width = canvas.height = 16;
        const context = canvas.getContext("2d");
        context.drawImage(image, 0, 0, 16, 16);
        const data = context.getImageData(0, 0, 16, 16).data;
        let sum = 0;
        let count = 0;
        for (let index = 0; index < data.length; index += 4) {
          if (data[index + 3] >= 128) {
            sum += luminance([data[index], data[index + 1], data[index + 2]]);
            count++;
          }
        }
        return count && sum / count < 0.18 ? "dark" : "light";
      })().catch(() => "light")
    );
  }
  return iconInks.get(source);
}

function markedText(doc, className, text, terms) {
  const node = doc.createElementNS("http://www.w3.org/1999/xhtml", "span");
  node.className = className;
  const pattern = new RegExp(`(${terms.map(term => term.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|")})`, "gi");
  text.split(terms.length ? pattern : /$^/).forEach((part, index) => {
    if (!part) {
      return;
    }
    if (index % 2) {
      const mark = doc.createElementNS("http://www.w3.org/1999/xhtml", "span");
      mark.className = "sly-search-mark";
      mark.textContent = part;
      node.append(mark);
    } else {
      node.append(part);
    }
  });
  return node;
}

async function getSearchGroups(win, terms, scope) {
  const hit = value => terms.every(term => value.toLowerCase().includes(term));
  const inSection = (label, prefix) => label === prefix || label.startsWith(`${prefix} / `);
  const spaceCrumb = (id, name) => name && { label: name, scope: { key: `space:${id}`, kind: "space", space: id, label: name } };
  const groupCrumb = group => ({ label: group.title, scope: { key: `group:${group.id}`, kind: "group", group: group.id, label: group.title } });
  const sectionCrumbs = (group, label) => {
    const levels = label.split(" / ");
    return levels
      .map((name, index) => ({ label: name, scope: { key: `section:${group.id}:${levels.slice(0, index + 1).join(" / ")}`, kind: "section", group: group.id, section: levels.slice(0, index + 1).join(" / "), label: name } }))
      .slice(-2);
  };
  const bookmarks = [];
  const marked = new Set();
  const scoped = new Set();
  for (const group of State.groups) {
    const space = win.gZenWorkspaces.getWorkspaceFromId(group.space)?.name;
    for (const section of group.sections) {
      const inside =
        !scope ||
        (scope.kind === "space" && group.space === scope.space) ||
        (scope.kind === "group" && group.id === scope.group) ||
        (scope.kind === "section" && group.id === scope.group && inSection(section.label ?? "", scope.section));
      for (const page of section.pages) {
        if (!page.verified || !inside) {
          continue;
        }
        scoped.add(page.url);
        const path = [space, group.shelf, group.title, section.label].filter(Boolean).join(" / ");
        if (!marked.has(page.url) && hit(`${page.title} ${page.url} ${path}`)) {
          marked.add(page.url);
          bookmarks.push({ title: page.title, url: page.url, icon: `page-icon:${page.url}`, path, crumbs: [spaceCrumb(group.space, space), groupCrumb(group), ...(section.label ? sectionCrumbs(group, section.label) : [])].filter(Boolean), group });
        }
      }
    }
  }
  const tabs = [];
  for (const owner of browserWindows()) {
    if (isPopup(owner) || !owner.gZenWorkspaces) {
      continue;
    }
    for (const tab of realTabs(owner)) {
      const url = tabUrl(tab);
      const id = tab.getAttribute("zen-workspace-id");
      const group = groupOfTab(tab) ?? groupOfPage(url, id);
      const section = group?.sections.find(entry => entry.label && entry.pages.some(page => page.url === url));
      const folder = tab.group?.label;
      const inside =
        !scope ||
        (scope.kind === "space" && id === scope.space) ||
        (scope.kind === "group" && group?.id === scope.group) ||
        (scope.kind === "section" && scoped.has(url)) ||
        (scope.kind === "folder" && folder === scope.folder);
      if (inside && hit(`${tab.label} ${url}`)) {
        const space = owner.gZenWorkspaces.getWorkspaceFromId(id)?.name;
        const crumbs = [
          spaceCrumb(id, space),
          ...(group ? [groupCrumb(group), ...(section ? sectionCrumbs(group, section.label) : [])] : [folder && { label: folder, scope: { key: `folder:${folder}`, kind: "folder", folder, label: folder } }]),
        ].filter(Boolean);
        tabs.push({ title: tab.label, url, icon: tab.image || `page-icon:${url}`, path: crumbs.map(crumb => crumb.label).join(" / "), crumbs, tab, owner });
      }
    }
  }
  const limit = scope ? 200 : 16;
  const groups = [{ label: "Bookmarks", rows: bookmarks.slice(0, limit) }, { label: "Open tabs", rows: tabs.slice(0, scope ? 200 : 12) }];
  if (scope) {
    return groups.filter(group => group.rows.length);
  }
  const db = await lazy.PlacesUtils.promiseDBConnection();
  const params = Object.fromEntries(terms.map((term, index) => [`t${index}`, `%${term.replace(/[\\%_]/g, match => "\\" + match)}%`]));
  const like = column => terms.map((_, index) => `lower(${column}) LIKE :t${index} ESCAPE '\\'`).join(" AND ");
  const folders = new Map();
  for (const row of await db.execute("SELECT id, parent, title, guid FROM moz_bookmarks WHERE type = 2")) {
    folders.set(row.getResultByName("id"), { parent: row.getResultByName("parent"), title: row.getResultByName("title"), guid: row.getResultByName("guid") });
  }
  const roots = {
    [lazy.PlacesUtils.bookmarks.menuGuid]: "Bookmarks Menu",
    [lazy.PlacesUtils.bookmarks.toolbarGuid]: "Bookmarks Toolbar",
    [lazy.PlacesUtils.bookmarks.unfiledGuid]: "Other Bookmarks",
    [lazy.PlacesUtils.bookmarks.mobileGuid]: "Mobile Bookmarks",
  };
  const folderPath = id => {
    const names = [];
    for (let folder = folders.get(id); folder && folder.guid !== lazy.PlacesUtils.bookmarks.rootGuid; folder = folders.get(folder.parent)) {
      names.unshift(roots[folder.guid] ?? folder.title ?? "");
    }
    return names.filter(Boolean).join(" / ");
  };
  const marks = await db.execute(
    `SELECT b.title AS name, b.parent, h.url, h.title FROM moz_bookmarks b JOIN moz_places h ON h.id = b.fk
     WHERE b.type = 1 AND ${like("coalesce(b.title, '') || ' ' || coalesce(h.title, '') || ' ' || h.url")}
     ORDER BY h.frecency DESC LIMIT 12`,
    params
  );
  for (const row of marks) {
    const url = row.getResultByName("url");
    if (!marked.has(url)) {
      marked.add(url);
      bookmarks.push({ title: row.getResultByName("name") || row.getResultByName("title") || "", url, icon: `page-icon:${url}`, time: folderPath(row.getResultByName("parent")) });
    }
  }
  groups[0].rows = bookmarks.slice(0, limit);
  const seen = new Set([...tabs.map(row => row.url), ...marked]);
  const visits = await db.execute(
    `SELECT url, title, last_visit_date FROM moz_places
     WHERE last_visit_date IS NOT NULL AND hidden = 0 AND ${like("coalesce(title, '') || ' ' || url")}
     ORDER BY frecency DESC LIMIT 40`,
    params
  );
  const today = new Date().setHours(0, 0, 0, 0);
  const days = new Map();
  [...visits]
    .map(row => ({ url: row.getResultByName("url"), title: row.getResultByName("title") ?? "", date: new Date(row.getResultByName("last_visit_date") / 1000) }))
    .filter(row => !seen.has(row.url))
    .sort((a, b) => b.date - a.date)
    .forEach(row => {
      const day = new Date(row.date).setHours(0, 0, 0, 0);
      const ago = Math.round((today - day) / 86400000);
      const label = ago === 0 ? "Today" : ago === 1 ? "Yesterday" : searchDay.format(row.date);
      days.set(label, [...(days.get(label) ?? []), { title: row.title, url: row.url, icon: `page-icon:${row.url}`, time: searchTime.format(row.date) }]);
    });
  return [...groups, ...[...days].map(([label, rows]) => ({ label, rows }))].filter(group => group.rows.length);
}

function pickSearchRow(win, row, event) {
  if (row.group) {
    postGroupTab(win, row.group, row.url).catch(logError);
    return;
  }
  if (!row.tab) {
    win.openTrustedLinkIn(row.url, event.metaKey || event.ctrlKey ? "tab" : "current");
    return;
  }
  const space = row.tab.getAttribute("zen-workspace-id");
  row.owner.focus();
  Promise.resolve(!space || row.owner.gZenWorkspaces.activeWorkspace === space || row.owner.gZenWorkspaces.changeWorkspaceWithID(space))
    .then(() => (row.owner.gBrowser.selectedTab = row.tab))
    .catch(logError);
}

function takeSpaceIconMenu(win) {
  const doc = win.document;
  const item = doc.getElementById("context_zenEditWorkspaceIcon");
  if (!item || item.dataset.sly) {
    return;
  }
  item.dataset.sly = "true";
  item.removeAttribute("command");
  item.addEventListener("command", () => {
    const trigger = doc.getElementById("zenWorkspaceMoreActions")?.triggerNode;
    const workspace = trigger?.closest?.("zen-workspace") ?? doc.getElementById(win.gZenWorkspaces.activeWorkspace);
    const anchor = workspace?.querySelector(".sly-space-icon");
    if (workspace && anchor) {
      openSpacePicker(win, workspace, anchor);
    }
  });
}

async function placeExternal(win, first, url) {
  let tab = first;
  if (!/^https?:/.test(url) || tab.pinned || tab.hasAttribute("zen-essential")) {
    return;
  }
  let group = groupOfPage(url, win.gZenWorkspaces.activeWorkspace);
  let space = group?.space ?? null;
  if (!group) {
    const match = await runSlybCommand(["rule", "match", url]);
    group = match?.group ? State.groups.find(entry => entry.id === match.group) : null;
    space = match?.space || group?.space || domainSpace(url);
    if (!space) {
      return;
    }
    if (group && match?.group) {
      await State.changeGroup(group.id, changed => {
        let section = changed.sections.find(entry => entry.label === match.section);
        if (!section) {
          section = { id: Math.random().toString(16).slice(2, 14), label: match.section, kind: "section", pages: [] };
          changed.sections.push(section);
        }
        section.pages.push({ url, title: tab.label, source: "rule", rule: match.id, rule_name: match.rule, verified: false, added_at: new Date().toISOString() });
      });
    }
  }
  if (space !== tab.getAttribute("zen-workspace-id") && win.gZenWorkspaces.getWorkspaceFromId(space)) {
    win.gZenWorkspaces.moveTabToWorkspace(tab, space);
    await win.gZenWorkspaces.changeWorkspaceWithID(space);
  }
  const context = group?.login ? loginContext(group.login) : getSpaceContext(win, tab.getAttribute("zen-workspace-id"));
  if (tab.userContextId !== context) {
    const moved = await postOpenPage(win, url, tab.getAttribute("zen-workspace-id"), { userContextId: context });
    win.gBrowser.removeTab(tab);
    tab = moved;
  }
  win.gBrowser.selectedTab = tab;
  if (!group) {
    return;
  }
  if (group.sections.some(section => section.pages.some(page => page.url === url)) && !pageTaken(win, tab, url, group.space)) {
    tagTab(tab, "sly-page", url);
  } else {
    tagTab(tab, "sly-group", group.id);
  }
  opened.add(`group:${group.id}`);
  expanded.add(`group:${group.id}`);
  await renderCards(win);
  win.setTimeout(() => {
    const card = win.document.querySelector(`.sly-card[data-key="group:${group.id}"]`);
    const row = card?.querySelector(".sly-card-tab[selected]") ?? card;
    const board = row?.closest(".sly-cards");
    if (board) {
      const offset = row.getBoundingClientRect().top - board.getBoundingClientRect().top;
      board.scrollTo({ top: board.scrollTop + offset - board.clientHeight / 2, behavior: "smooth" });
    }
  }, 600);
}

function routeExternal(win) {
  const manager = lazy.gZenSpaceRoutingManager;
  if (manager._slyRouted) {
    return;
  }
  manager._slyRouted = true;
  const original = manager.onAfterAddTab.bind(manager);
  manager.onAfterAddTab = (uriString, newTab, options, owner, beforeResult) => {
    original(uriString, newTab, options, owner, beforeResult);
    const started = owner?._slyNewTabGroup;
    if (started && owner.gURLBar.hasAttribute("zen-newtab") && /^https?:/.test(uriString)) {
      owner._slyNewTabGroup = null;
      postGroupTab(owner, started, uriString, newTab).catch(logError);
    } else if (options?.fromExternal) {
      placeExternal(owner, newTab, uriString).catch(logError);
    }
  };
}

function parseColor(value) {
  if (!value) {
    return null;
  }
  if (dimmedColors[value]) {
    return dimmedColors[value];
  }
  const hex = /^#?([0-9a-f]{6})$/i.exec(value)?.[1];
  return hex ? [0, 2, 4].map(index => parseInt(hex.slice(index, index + 2), 16)) : null;
}

function dimColor(rgb) {
  return rgb.map(value => Math.round(value * 0.55 + 38));
}

async function faviconPalette(win, icon) {
  if (!icon || !/^https?:/.test(icon)) {
    return null;
  }
  const source = siteIcon(`${icon}/`);
  if (source === defaultIcon) {
    return null;
  }
  if (!colorCache.has(source)) {
    colorCache.set(
      source,
      (async () => {
        const image = new win.Image();
        image.src = source;
        await image.decode();
        const canvas = win.document.createElementNS("http://www.w3.org/1999/xhtml", "canvas");
        canvas.width = canvas.height = 24;
        const context = canvas.getContext("2d");
        context.drawImage(image, 0, 0, 24, 24);
        const data = context.getImageData(0, 0, 24, 24).data;
        const buckets = new Map();
        for (let index = 0; index < data.length; index += 4) {
          if (data[index + 3] < 128) {
            continue;
          }
          const [r, g, b] = [data[index], data[index + 1], data[index + 2]];
          const max = Math.max(r, g, b);
          const min = Math.min(r, g, b);
          const weight = 0.1 + (max === 0 ? 0 : (max - min) / max) * 2;
          const key = `${r >> 5},${g >> 5},${b >> 5}`;
          const bucket = buckets.get(key) ?? { r: 0, g: 0, b: 0, w: 0 };
          bucket.r += r * weight;
          bucket.g += g * weight;
          bucket.b += b * weight;
          bucket.w += weight;
          buckets.set(key, bucket);
        }
        const colors = [...buckets.values()].sort((a, b) => b.w - a.w).map(bucket => [bucket.r / bucket.w, bucket.g / bucket.w, bucket.b / bucket.w].map(Math.round)).filter(color => Math.min(...color) < 225);
        if (!colors.length) {
          return null;
        }
        const distance = (a, b) => Math.hypot(a[0] - b[0], a[1] - b[1], a[2] - b[2]);
        const distinct = [];
        for (const color of colors) {
          if (distinct.every(kept => distance(kept, color) > 80)) {
            distinct.push(color);
          }
          if (distinct.length === 3) {
            break;
          }
        }
        return distinct;
      })().catch(() => null)
    );
  }
  return colorCache.get(source);
}

async function applyTheme(win) {
  const space = win.gZenWorkspaces.getActiveWorkspace();
  const workspace = space && win.document.getElementById(space.uuid);
  if (!space || !workspace?.hasAttribute("sly-cards")) {
    return;
  }
  const palette = (await faviconPalette(win, workspaceIcon(win.document, space.uuid)))?.map(dimColor) ?? null;
  const key = `space:${space.uuid}:${palette?.join("|") ?? "own"}`;
  if (key === win._slyThemeKey) {
    return;
  }
  win._slyThemeKey = key;
  State.themeColors = palette ?? (space.theme?.gradientColors ?? []).map(color => color.c).filter(Array.isArray);
  if (palette) {
    win.gZenThemePicker.onWorkspaceChange(space, false, themeFromColors(palette));
  } else {
    win.gZenThemePicker.onWorkspaceChange(space);
  }
  win.setTimeout(() => {
    const colors = State.themeColors.length ? State.themeColors : [[48, 38, 46]];
    const tint = [0, 1, 2].map(index => colors.reduce((sum, color) => sum + color[index], 0) / colors.length);
    const dark = win.matchMedia("(prefers-color-scheme: dark)").matches;
    const under = tint.map(value => Math.round(value * themeOpacity + (dark ? 27 : 235) * (1 - themeOpacity)));
    win.document.documentElement.style.setProperty("--sly-under", `rgb(${under.join(" ")})`);
    setSidebarInk(win, under);
    sampleSidebar(win);
    if (under.join() !== sidebarBase.join()) {
      sidebarBase = under;
      renderCards(win);
    }
  }, 700);
}

function openClearDialog(win, anchor) {
  const uri = win.gBrowser.currentURI;
  let site = "";
  try {
    site = Services.eTLD.getSchemelessSite(uri);
  } catch {
    site = uri?.host ?? "";
  }
  if (!site || !/^https?$/.test(uri?.scheme ?? "")) {
    settle(win, anchor, "failed", "This page has no site data to clear");
    return;
  }
  openWarning(win, anchor, {
    title: `Clear data for ${site}?`,
    text: `This signs you out of ${site} and removes its cookies, site data and cached files, in every tab. Other sites are not touched. This cannot be undone.`,
    action: "Clear data",
    run: () => {
      const flags = Ci.nsIClearDataService.CLEAR_COOKIES_AND_SITE_DATA | Ci.nsIClearDataService.CLEAR_ALL_CACHES;
      Services.clearData.deleteDataFromSite(site, {}, true, flags, {
        onDataDeleted(failed) {
          settle(win, anchor, failed ? "failed" : "done", failed ? `Some data for ${site} could not be cleared` : `Cleared ${site}`);
          win.gBrowser.reloadTab(win.gBrowser.selectedTab);
        },
      });
    },
  });
}

function openWarning(win, anchor, { title, text, action, run }) {
  const doc = win.document;
  const panel = iconPanel(doc);
  const cancel = element(doc, "button", "sly-icon-button", { label: "Cancel" });
  cancel.addEventListener("command", () => panel.hidePopup());
  const confirm = element(doc, "button", "sly-icon-button sly-danger-button", { label: action });
  confirm.addEventListener("command", () => {
    panel.hidePopup();
    run();
  });
  const footer = element(doc, "hbox", "sly-icon-footer");
  footer.append(cancel, confirm);
  const head = element(doc, "hbox", "sly-warning-head", { align: "center" });
  const heading = element(doc, "description", "sly-icon-title");
  heading.textContent = title;
  head.append(element(doc, "image", "sly-warning-icon"), heading);
  panel.replaceChildren(head, Object.assign(element(doc, "description", "sly-icon-hint sly-warning-text"), { textContent: text }), footer);
  panel.openPopup(anchor, "after_end");
}

function dialogPanel(doc) {
  let panel = doc.getElementById("sly-dialog");
  if (!panel) {
    panel = element(doc, "panel", "sly-icon-panel sly-dialog", { id: "sly-dialog", orient: "vertical", noautofocus: "true" });
    doc.getElementById("mainPopupSet").append(panel);
  }
  return panel;
}

function openSpaceDialog(win) {
  const doc = win.document;
  const panel = dialogPanel(doc);
  const html = name => doc.createElementNS("http://www.w3.org/1999/xhtml", name);
  const input = html("input");
  input.className = "sly-space-name";
  input.placeholder = "Name, such as Work";
  const domainInput = html("input");
  domainInput.className = "sly-space-name";
  domainInput.placeholder = "Domain, such as example.com";
  let color = "blue";
  let icon = "";
  const swatches = element(doc, "hbox", "sly-swatches");
  for (const [name, rgb] of Object.entries(dimmedColors)) {
    const swatch = element(doc, "box", "sly-swatch", { tooltiptext: name });
    swatch.style.backgroundColor = `rgb(${rgb.join(" ")})`;
    swatch.toggleAttribute("selected", name === color);
    swatch.addEventListener("click", () => {
      color = name;
      for (const other of swatches.children) {
        other.toggleAttribute("selected", other === swatch);
      }
    });
    swatches.append(swatch);
  }
  const iconButton = element(doc, "button", "sly-icon-button", { label: "Icon…", tooltiptext: "Pick an emoji or a symbol for the space" });
  iconButton.addEventListener("command", () => {
    win.gZenEmojiPicker.open(iconButton, {
      closeOnSelect: true,
      allowNone: true,
      onSelect: picked => {
        icon = picked ?? "";
        iconButton.setAttribute("label", icon && !/^(chrome|resource):/.test(icon) ? icon : icon ? "Icon set" : "Icon…");
      },
    });
  });
  const cancel = element(doc, "button", "sly-icon-button", { label: "Cancel" });
  cancel.addEventListener("command", () => panel.hidePopup());
  const create = element(doc, "button", "sly-icon-button sly-primary-button", { label: "Create workspace" });
  const submit = async () => {
    const name = input.value.trim();
    if (!name) {
      input.focus();
      return;
    }
    panel.hidePopup();
    const space = await win.gZenWorkspaces.createAndSaveWorkspace(name, icon || undefined, true);
    putSpaceLook(win, win.gZenWorkspaces.getWorkspaceFromId(space.uuid), { color });
    const domain = domainInput.value.trim();
    if (domain) {
      runSlybCommand(["workspace", "domain", "create", space.uuid, domain]).catch(logError);
    }
    await win.gZenWorkspaces.changeWorkspaceWithID(space.uuid);
  };
  create.addEventListener("command", () => submit().catch(logError));
  input.addEventListener("keydown", event => {
    if (event.key === "Enter") {
      submit().catch(logError);
    }
  });
  const footer = element(doc, "hbox", "sly-icon-footer");
  footer.append(cancel, create);
  const row = element(doc, "hbox", "sly-space-row", { align: "center" });
  row.append(iconButton);
  panel.replaceChildren(
    element(doc, "label", "sly-icon-title", { value: "New workspace" }),
    Object.assign(element(doc, "description", "sly-icon-hint"), { textContent: "A workspace has its own profile, the logins that stay inside it. Its domain gives it its icon and theme, and pages of that domain opened from outside land in it." }),
    input,
    domainInput,
    element(doc, "label", "sly-field-label", { value: "Color" }),
    swatches,
    row,
    footer
  );
  panel.openPopupAtScreen(Math.round(win.mozInnerScreenX + win.innerWidth / 2 - 170), Math.round(win.mozInnerScreenY + win.innerHeight / 4), false);
  win.setTimeout(() => input.focus(), 50);
}

function aiCommands(scope) {
  const wide = scope[0] === "--workspace" || scope[0] === "--project";
  return [
    ["Organize", ["organize", ...scope], "Arrange its pages into sections and place loose tabs that belong"],
    ["Find similar pages", wide ? ["organize", ...scope, "--history"] : ["group", "similar", scope[1], ...scope.slice(2)], "Pick pages that belong from your open tabs and pages visited more than once"],
    ["Summarize visits", ["summary", "create", ...scope], "Summarize how these pages were visited, into the AI area of the space"],
    ["Arrange by prompt…", ["organize", ...scope, "--prompt"], scope[0] === "--workspace" ? "Order the groups and put them on shelves the way you describe" : "Sort and relate these pages into sections the way you describe"],
  ];
}

function aiMenu(win, title, key, scope, extra = () => []) {
  const doc = win.document;
  const button = element(doc, "toolbarbutton", "sly-card-more", { tooltiptext: `More for ${title}` });
  button.dataset.tooltip = `More for ${title}`;
  menuButton(button, () => {
    const actions = scope
      ? aiCommands(scope).map(([label, args, tooltip]) => {
          const item = element(doc, "menuitem", "", { label, tooltiptext: tooltip });
          item.addEventListener("command", () => (args.at(-1) === "--prompt" ? openPromptDialog(win, arrangeText(title), text => runSlyb(win, [...args, text], button, key)) : runSlyb(win, args, button, key)));
          return item;
        })
      : [];
    const parts = [extra()];
    if (actions.length) {
      const rules = element(doc, "menuitem", "", { label: "Rules…", tooltiptext: `Rules that place pages opened from outside the browser in ${title}` });
      rules.addEventListener("command", () => openRulesDialog(win, scope, title));
      const header = label => element(doc, "menuitem", "sly-menu-header", { label, disabled: "true" });
      const forget = element(doc, "menuitem", "", { label: "Remove from Claude", tooltiptext: `Forget the pages of ${title} from what Claude taught the browser, everywhere` });
      forget.addEventListener("command", () => runSlyb(win, ["forgotten", "create", ...scope], button, key));
      const project = scope[0] === "--project";
      const remove = (label, from, tooltip) => {
        const item = element(doc, "menuitem", "", { label, tooltiptext: tooltip });
        item.addEventListener("command", () =>
          openWarning(win, button, {
            title: `${label} ${title}?`,
            text: `${title} leaves the sidebar for good${from === "both" ? ", Claude forgets its pages and searches," : ""} and every page of its history under its addresses is deleted. This cannot be undone.`,
            action: label,
            run: () => {
              const card = button.closest(".sly-card");
              vanish(card);
              runSlyb(win, ["forgotten", "create", ...scope, "--from", from, "--history"], button, key).then(done => done || card?.removeAttribute("sly-gone"));
            },
          })
        );
        return item;
      };
      const projectItems = [forget, remove("Remove from sidebar", "sidebar", "Hide the project and delete its history"), remove("Remove from Claude and sidebar", "both", "Forget it in Claude, hide it and delete its history")];
      const group = scope[0] === "--group" && !scope.includes("--section") ? State.groups.find(entry => entry.id === scope[1]) : null;
      const separate = group && !group.login ? element(doc, "menuitem", "", { label: "Separate login…", tooltiptext: "Give this group a login of its own, so its pages keep their own sessions" }) : null;
      separate?.addEventListener("command", () =>
        openPromptDialog(win, { title: `Separate login for ${group.title}`, hint: "Its pages open in a container of their own with this name, so you can stay signed in to another account here.", placeholder: "Such as Claude work", action: "Separate", value: group.title }, text => runSlyb(win, ["group", "update", group.id, "--login", text], button, key))
      );
      const drop = (label, tooltip) => {
        const item = element(doc, "menuitem", "", { label, tooltiptext: tooltip });
        item.addEventListener("command", () => {
          const tabs = browserWindows().flatMap(other => realTabs(other)).filter(tab => groupOfTab(tab)?.id === group.id);
          openWarning(win, button, {
            title: `${label.replace("…", "")} ${group.title}?`,
            text: `${group.title} and its pages leave the sidebar. ${tabs.length ? `Its ${tabs.length} open ${tabs.length === 1 ? "tab closes" : "tabs close"}.` : ""} This cannot be undone.`,
            action: label.replace("…", ""),
            run: () => {
              const card = button.closest(".sly-card");
              vanish(card);
              for (const tab of tabs) {
                tab.ownerGlobal.gBrowser.removeTab(tab);
              }
              runSlyb(win, ["group", "delete", group.id], button, key).then(done => done || card?.removeAttribute("sly-gone"));
            },
          });
        });
        return item;
      };
      const dropItems = group ? [drop("Delete group…", "Delete the group and its pages, and close its open tabs")] : [];
      parts.push([header("Assistant"), ...actions], project ? [header("Project"), ...projectItems] : [header(scope.includes("--section") ? "Section" : "Group"), rules, ...(separate ? [separate] : []), forget, ...dropItems]);
      if (group?.login) {
        parts.push(containerItems(win, group, button, key));
      }
    }
    return parts.filter(part => part.length).flatMap((part, index) => (index ? [element(doc, "menuseparator"), ...part] : part));
  });
  button.addEventListener("click", stopEvent);
  return button;
}

function containerItems(win, group, button, key) {
  const doc = win.document;
  const service = lazy.ContextualIdentityService;
  const context = loginContext(group.login);
  const identity = service.getPublicIdentityFromId(context);
  const tabs = browserWindows().flatMap(other => [...other.gZenWorkspaces.allStoredTabs]).filter(tab => tab.userContextId === context);
  const sites = [...new Set(Services.cookies.getCookiesWithOriginAttributes(JSON.stringify({ userContextId: context })).map(cookie => cookie.rawHost.replace(/^www\./, "")))].sort();
  const status = element(doc, "menuitem", "", {
    label: `${tabs.length} open ${tabs.length === 1 ? "tab" : "tabs"} · cookies from ${sites.length} ${sites.length === 1 ? "site" : "sites"}`,
    tooltiptext: sites.length ? sites.join(", ") : "Nothing is signed in here yet",
    disabled: "true",
  });
  const rename = element(doc, "menuitem", "", { label: "Rename container…", tooltiptext: "Rename the container; its logins stay" });
  rename.addEventListener("command", () =>
    openPromptDialog(win, { title: `Rename ${group.login}`, hint: "Its logins and cookies stay; only the name changes.", placeholder: "Such as Claude work", action: "Rename", value: group.login }, text => {
      service.update(context, text, identity.icon, identity.color);
      return runSlyb(win, ["group", "update", group.id, "--login", text], button, key);
    })
  );
  const colors = element(doc, "menu", "", { label: "Color" });
  const palette = doc.createXULElement("menupopup");
  palette.append(
    ...radioItems(doc, `sly-login-color-${group.id}`, [...loginColors, "toolbar"].map(color => [color, color === "toolbar" ? "None" : color[0].toUpperCase() + color.slice(1)]), identity.color, color => {
      service.update(context, group.login, identity.icon, color);
      renderCards(win);
    })
  );
  colors.append(palette);
  const signOut = element(doc, "menuitem", "", { label: "Sign out of everything in it…", tooltiptext: "Delete the cookies and site data of this container; its tabs reload signed out" });
  signOut.addEventListener("command", () =>
    openWarning(win, button, {
      title: `Sign out of ${group.login}?`,
      text: `Every cookie and all site data of the container ${group.login} is deleted, so each site in it asks you to sign in again.`,
      action: "Sign out",
      run: () => Services.clearData.deleteDataFromOriginAttributesPattern({ userContextId: context }, { onDataDeleted: () => tabs.forEach(tab => tab.ownerGlobal.gBrowser.reloadTab(tab)) }),
    })
  );
  const workspace = element(doc, "menuitem", "", { label: "Use the workspace login", tooltiptext: "Open its pages with the workspace's own login again; this container keeps its cookies" });
  workspace.addEventListener("command", () => runSlyb(win, ["group", "update", group.id, "--login", ""], button, key));
  return [element(doc, "menuitem", "sly-menu-header", { label: `Container ${group.login}`, disabled: "true" }), status, rename, colors, signOut, workspace];
}

function takeSpaceMenu(win) {
  const doc = win.document;
  const popup = doc.getElementById("zenWorkspaceMoreActions");
  if (!popup || popup.dataset.sly) {
    return;
  }
  popup.dataset.sly = "true";
  popup.addEventListener("popupshowing", event => {
    if (event.target !== popup) {
      return;
    }
    const trigger = popup.triggerNode;
    const chosen = trigger?.closest?.("[zen-workspace-id]")?.getAttribute("zen-workspace-id") ?? trigger?.closest?.("zen-workspace")?.id ?? win.gZenWorkspaces.activeWorkspace;
    const workspace = doc.getElementById(chosen);
    if (!workspace?.hasAttribute("sly-cards")) {
      return;
    }
    event.preventDefault();
    const anchor = trigger?.closest?.("toolbarbutton") ?? workspace.querySelector(".zen-workspaces-actions");
    win.setTimeout(() => openSpaceMenu(win, workspace, null, anchor), 0);
  });
}

function menuPanel(doc) {
  let panel = doc.getElementById("sly-menu");
  if (!panel) {
    panel = element(doc, "panel", "sly-menu", { id: "sly-menu", type: "arrow", orient: "vertical", noautofocus: "true" });
    doc.getElementById("mainPopupSet").append(panel);
  }
  return panel;
}

function openSpaceMenu(win, workspace, sidebar, anchor) {
  const doc = win.document;
  const panel = menuPanel(doc);
  const space = workspace.id;
  const current = win.gZenWorkspaces.getWorkspaceFromId(space);
  const name = current?.name ?? "the workspace";
  const header = label => element(doc, "label", "sly-menu-header", { value: label });
  const row = (label, tooltip, run, attributes = {}) => {
    const item = element(doc, "toolbarbutton", "sly-menu-row", { label, tooltiptext: tooltip, ...attributes });
    item.addEventListener("command", () => {
      panel.hidePopup();
      run();
    });
    return item;
  };
  const column = (title, rows) => {
    const box = element(doc, "vbox", "sly-menu-column");
    box.append(header(title), ...rows);
    return box;
  };
  const zenRow = (label, id, tooltip) =>
    row(label, tooltip, async () => {
      if (win.gZenWorkspaces.activeWorkspace !== space) {
        await win.gZenWorkspaces.changeWorkspaceWithID(space);
      }
      doc.getElementById(id)?.doCommand();
    });
  const assistant = aiCommands(["--workspace", space]).map(([label, args, tooltip]) =>
    row(label, tooltip, () => (args.at(-1) === "--prompt" ? openPromptDialog(win, arrangeText(name), text => runSlyb(win, [...args, text], anchor, `space:${space}`)) : runSlyb(win, args, anchor, `space:${space}`)))
  );
  assistant.push(
    row("Build a space…", "Describe a space and the assistant builds it from your pages, history, Claude sessions and repositories", () =>
      openPromptDialog(win, { title: "Build a space", hint: "Say what it should hold. It is filled from your stored pages, history, Claude sessions and local repositories, with a rule per group.", placeholder: "Such as work: a group for the app listings per environment, one for github with its repos", action: "Build" }, text =>
        runSlyb(win, ["space", "build", text, "--workspace", space], anchor, `space:${space}`)
      )
    )
  );
  const profile = current?.containerTabId ? lazy.ContextualIdentityService.getUserContextLabel(current.containerTabId) : "";
  const tabs = Array.from(workspace.querySelectorAll("tab")).filter(tab => win.gBrowser.isTab(tab) && !tab.hasAttribute("zen-empty-tab"));
  const spaces = (State.spaces ?? []).filter(entry => entry.workspace === space);
  const groups = (State.groups ?? []).filter(group => spaces.some(entry => entry.id === group.space_id));
  const workspaceRows = [
    zenRow("Rename…", "context_zenEditWorkspace", "Rename the workspace and its profile"),
    row("Domain…", "The workspace's domain: its icon and theme, and where pages of that domain opened from outside land", () =>
      openPromptDialog(win, { title: `Domain of ${name}`, hint: "Its favicon becomes the workspace icon and theme, and related pages opened from outside land here.", placeholder: "Such as example.com", action: "Set domain", value: State.spaceTies?.[space]?.domains?.[0] ?? "" }, text =>
        runSlyb(win, ["workspace", "domain", "create", space, text], anchor, `space:${space}`)
      )
    ),
    zenRow("Theme…", "context_zenChangeWorkspaceTheme", "Change the theme of the workspace"),
    row("Rules…", "Rules that place pages opened from outside the browser in this workspace", () => openRulesDialog(win, ["--workspace", space], name)),
    row("Remove from Claude", "Forget the pages of this workspace from what Claude taught the browser, everywhere", () => runSlyb(win, ["forgotten", "create", "--workspace", space], anchor, `space:${space}`)),
    element(doc, "toolbarbutton", "sly-menu-row", { label: profile ? `Profile ${profile}, its logins` : "No profile yet", disabled: "true" }),
    row(
      "Delete workspace…",
      `Delete the workspace ${name} with its ${tabs.length} open tabs, its ${spaces.length} spaces and their ${groups.length} groups; its profile keeps its logins`,
      () =>
        openWarning(win, anchor, {
          title: `Delete the workspace ${name}?`,
          text: `Its ${tabs.length} open tabs close, ${tabs.filter(tab => tab.pinned).length} of them pinned. Its ${spaces.length} spaces go with it, and their ${groups.length} groups with their pages. ${profile ? `The profile ${profile} keeps its logins. ` : ""}This cannot be undone.`,
          action: "Delete workspace",
          run: async () => {
            await State.loadGroups();
            const gone = new Set(State.spaces.filter(entry => entry.workspace === space).map(entry => entry.id));
            State.groups = State.groups.filter(group => !gone.has(group.space_id));
            State.spaces = State.spaces.filter(entry => !gone.has(entry.id));
            await State.saveGroups();
            Services.prefs.setStringPref(pinnedPref, JSON.stringify(JSON.parse(Services.prefs.getStringPref(pinnedPref, "[]")).filter(app => app.space !== space)));
            await win.gZenWorkspaces.removeWorkspace(space);
          },
        }),
      { danger: "true" }
    ),
  ];
  const workspaces = win.gZenWorkspaces.getWorkspaces().map(entry => row(entry.name, `Switch to ${entry.name}`, () => win.gZenWorkspaces.changeWorkspaceWithID(entry.uuid), entry.uuid === win.gZenWorkspaces.activeWorkspace ? { checked: "true" } : {}));
  workspaces.push(row("New workspace…", "Create a workspace with its own profile", () => openSpaceDialog(win)));
  const columns = element(doc, "hbox", "sly-menu-columns");
  columns.append(column("Assistant", assistant));
  if (sidebar) {
    columns.append(column(`Space ${sidebar.name}`, sidebarSpaceRows(win, sidebar, workspace, anchor, row, header)));
  } else {
    columns.append(column(`Workspace ${name}`, workspaceRows), column("Workspaces", workspaces));
  }
  panel.replaceChildren(columns);
  iconize(panel);
  panel.openPopup(anchor, "after_end");
}

function aiArea(win, space) {
  const doc = win.document;
  const summaries = State.summaries.filter(summary => summary.space === space);
  if (summaries.length === 0) {
    return [];
  }
  const key = `ai:${space}`;
  let folded = !!cardState(key).collapsed;
  const area = element(doc, "vbox", "sly-ai-area");
  area.toggleAttribute("folded", folded);
  const head = element(doc, "hbox", "sly-ai-head", { align: "center", tooltiptext: "Collapse or expand the AI area" });
  head.append(
    element(doc, "image", "sly-ai-icon"),
    element(doc, "label", "sly-ai-title", { value: "AI", flex: "1" }),
    element(doc, "label", "sly-card-count", { value: String(summaries.length), tooltiptext: `${summaries.length} ${summaries.length === 1 ? "summary" : "summaries"}` }),
    element(doc, "image", "sly-card-chevron")
  );
  const body = element(doc, "vbox", "sly-ai-body");
  body.style.maxHeight = folded ? "0px" : "none";
  head.addEventListener("click", () => {
    folded = !folded;
    area.toggleAttribute("folded", folded);
    animateBody(body, !folded);
    putCardState(key, { collapsed: folded });
  });
  for (const summary of summaries) {
    body.append(summaryItem(win, space, summary));
  }
  area.append(head, body);
  return [area];
}

function summaryItem(win, space, summary) {
  const doc = win.document;
  const key = `summary:${summary.id}`;
  let open = !!cardState(key).open;
  const item = element(doc, "vbox", "sly-summary");
  item.toggleAttribute("sly-open", open);
  const row = element(doc, "hbox", "sly-card-tab sly-summary-row", { align: "center" });
  const when = new Date(summary.created_at).toLocaleDateString(undefined, { day: "numeric", month: "short" });
  row.append(
    element(doc, "image", "sly-card-icon sly-summary-icon"),
    element(doc, "label", "sly-card-label", { value: summary.title, crop: "end", flex: "1", tooltiptext: `${summary.title}\n${summary.target} · ${when}` }),
    element(doc, "label", "sly-card-count", { value: when, tooltiptext: `Summarized ${new Date(summary.created_at).toLocaleString()}` })
  );
  const close = element(doc, "image", "sly-card-close", { tooltiptext: "Remove this summary" });
  close.addEventListener("click", async event => {
    stopEvent(event);
    State.summaries = State.summaries.filter(entry => entry.id !== summary.id);
    await State.saveSummaries();
    renderCards(win);
  });
  row.append(close);
  const details = element(doc, "vbox", "sly-summary-details");
  details.style.maxHeight = open ? "none" : "0px";
  details.append(Object.assign(element(doc, "description", "sly-summary-overview"), { textContent: summary.overview }));
  for (const point of summary.points ?? []) {
    const bullet = element(doc, "hbox", "sly-card-tab sly-summary-point", { align: "center" });
    bullet.append(
      iconImage(doc, "sly-card-icon", siteIcon(point.url)),
      Object.assign(element(doc, "description", "sly-summary-text", { flex: "1", tooltiptext: `${point.title}\n${point.url}` }), { textContent: point.text })
    );
    bullet.addEventListener("click", () => postOpenPage(win, point.url, space));
    details.append(bullet);
  }
  row.addEventListener("click", () => {
    open = !open;
    item.toggleAttribute("sly-open", open);
    animateBody(details, open);
    putCardState(key, { open });
  });
  item.append(row, details);
  return item;
}

const paletteAlpha = 0.34;
let sidebarBase = [48, 38, 46];

function luminance(rgb) {
  const [r, g, b] = rgb.map(value => {
    const channel = value / 255;
    return channel <= 0.03928 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function paintPalette(node, palette) {
  if (!palette?.length) {
    return;
  }
  const [first, second = first, third = second] = palette;
  node.style.setProperty("--sly-c1", first.join(" "));
  node.style.setProperty("--sly-c2", second.join(" "));
  node.style.setProperty("--sly-c3", third.join(" "));
  const seen = first.map((value, index) => value * paletteAlpha + sidebarBase[index] * (1 - paletteAlpha));
  const ground = luminance(seen);
  const contrast = ink => (Math.max(ground, ink) + 0.05) / (Math.min(ground, ink) + 0.05);
  node.setAttribute("sly-ink", contrast(luminance([22, 16, 24])) > contrast(luminance([252, 248, 250])) ? "dark" : "light");
  node.setAttribute("sly-painted", "true");
}

function openPromptDialog(win, { title, hint, placeholder, action, value = "" }, onSubmit) {
  const doc = win.document;
  const panel = dialogPanel(doc);
  const input = doc.createElementNS("http://www.w3.org/1999/xhtml", "input");
  input.className = "sly-space-name";
  input.placeholder = placeholder;
  input.value = value;
  const cancel = element(doc, "button", "sly-icon-button", { label: "Cancel" });
  cancel.addEventListener("command", () => panel.hidePopup());
  const arrange = element(doc, "button", "sly-icon-button sly-primary-button", { label: action });
  const submit = () => {
    const text = input.value.trim();
    if (!text) {
      input.focus();
      return;
    }
    panel.hidePopup();
    onSubmit(text);
  };
  arrange.addEventListener("command", submit);
  input.addEventListener("keydown", event => {
    if (event.key === "Enter") {
      submit();
    }
  });
  const footer = element(doc, "hbox", "sly-icon-footer");
  footer.append(cancel, arrange);
  panel.replaceChildren(
    element(doc, "label", "sly-icon-title", { value: title }),
    Object.assign(element(doc, "description", "sly-icon-hint"), { textContent: hint }),
    input,
    footer
  );
  panel.openPopupAtScreen(Math.round(win.mozInnerScreenX + win.innerWidth / 2 - 170), Math.round(win.mozInnerScreenY + win.innerHeight / 4), false);
  win.setTimeout(() => input.focus(), 50);
}

async function openRulesDialog(win, scope, title) {
  const doc = win.document;
  const panel = dialogPanel(doc);
  const value = flag => scope[scope.indexOf(flag) + 1];
  const scopeOf = rule =>
    scope.includes("--section")
      ? rule.scope === "section" && rule.group === value("--group") && rule.section === value("--section")
      : scope.includes("--group")
        ? rule.scope === "group" && rule.group === value("--group")
        : rule.scope === "space" && rule.space === value("--workspace");
  const list = element(doc, "vbox", "sly-rule-list");
  const fill = async () => {
    const rules = ((await runSlybCommand(["rule", "get"])) ?? []).filter(scopeOf);
    list.replaceChildren(
      ...(rules.length
        ? rules.map(rule => {
            const row = element(doc, "hbox", "sly-rule-row", { align: "center", tooltiptext: rule.script });
            const remove = element(doc, "image", "sly-card-close", { tooltiptext: `Remove ${rule.name}` });
            remove.addEventListener("click", () => runSlybCommand(["rule", "delete", rule.id]).then(fill, logError));
            row.append(element(doc, "label", "sly-rule-name", { value: rule.name, flex: "1", crop: "end" }), remove);
            return row;
          })
        : [Object.assign(element(doc, "description", "sly-icon-hint"), { textContent: "No rules yet." })])
    );
  };
  const input = doc.createElementNS("http://www.w3.org/1999/xhtml", "input");
  input.className = "sly-space-name";
  input.placeholder = "What it should catch, such as the tst hosts of the app";
  const close = element(doc, "button", "sly-icon-button", { label: "Close" });
  close.addEventListener("command", () => panel.hidePopup());
  const add = element(doc, "button", "sly-icon-button sly-primary-button", { label: "Add rule" });
  const submit = async () => {
    const text = input.value.trim();
    if (!text) {
      input.focus();
      return;
    }
    add.setAttribute("disabled", "true");
    add.setAttribute("label", "Writing…");
    try {
      await runSlybCommand(["rule", "create", ...scope, "--ask", text]);
      input.value = "";
      await fill();
    } catch (error) {
      logError(error);
      add.setAttribute("label", "Failed, try again");
      return;
    } finally {
      add.removeAttribute("disabled");
    }
    add.setAttribute("label", "Add rule");
  };
  add.addEventListener("command", () => submit());
  input.addEventListener("keydown", event => {
    if (event.key === "Enter") {
      submit();
    }
  });
  const footer = element(doc, "hbox", "sly-icon-footer");
  footer.append(close, add);
  panel.replaceChildren(
    element(doc, "label", "sly-icon-title", { value: `Rules for ${title}` }),
    Object.assign(element(doc, "description", "sly-icon-hint"), { textContent: "A page opened from outside the browser that a rule catches opens here, among the unverified pages when the rule is a group's or a section's." }),
    list,
    input,
    footer
  );
  panel.openPopupAtScreen(Math.round(win.mozInnerScreenX + win.innerWidth / 2 - 170), Math.round(win.mozInnerScreenY + win.innerHeight / 4), false);
  win.setTimeout(() => input.focus(), 50);
  await fill();
}

function toast(win, anchor, text) {
  const tip = win.document.getElementById("sly-tip");
  if (!tip) {
    return;
  }
  const box = anchor.getBoundingClientRect();
  tip.textContent = text;
  tip.style.left = `${Math.max(4, box.left + box.width / 2 - tip.offsetWidth / 2)}px`;
  tip.style.top = `${box.bottom + 6}px`;
  tip.setAttribute("shown", "true");
  win.clearTimeout(tip._slyToast);
  tip._slyToast = win.setTimeout(() => tip.removeAttribute("shown"), 1400);
}

function openRowMenu(win, space, row, page, event) {
  const doc = win.document;
  let menu = doc.getElementById("sly-row-menu");
  if (!menu) {
    menu = doc.createXULElement("menupopup");
    menu.id = "sly-row-menu";
    doc.getElementById("mainPopupSet").append(menu);
  }
  const host = URL.parse(page.url)?.host ?? "";
  let domain = host;
  try {
    domain = Services.eTLD.getBaseDomainFromHost(host);
  } catch {}
  const header = label => element(doc, "menuitem", "sly-menu-header", { label, disabled: "true" });
  const fromHistory = !row.closest(".sly-card")?.dataset.key?.startsWith("group:");
  const remove = (label, tooltip, steps) => {
    const item = element(doc, "menuitem", "", { label, tooltiptext: tooltip });
    item.addEventListener("command", async () => {
      const gone = steps.includes("claude") || steps.includes("group") || fromHistory;
      if (gone) {
        vanish(row);
      }
      if (steps.includes("group")) {
        const tab = row._slyTab;
        for (const key of ["sly-page", "sly-group"]) {
          if (tab?.hasAttribute(key)) {
            tab.removeAttribute(key);
            lazy.SessionStore.deleteCustomTabValue(tab, key);
          }
        }
        if (steps.includes("close") && tab) {
          win.gBrowser.removeTab(tab);
        }
        const group = State.groups.find(entry => entry.id === ownGroup);
        if (group) {
          await dismissPage(win, { group }, page);
        }
      }
      let done = true;
      if (steps.includes("claude")) {
        done = await runSlyb(win, ["forgotten", "create", page.url], row, null);
      }
      if (done && steps.includes("history")) {
        done = await runSlyb(win, ["history", "delete", page.url], row, null);
      }
      if (!done && gone) {
        row.removeAttribute("sly-gone");
      }
    });
    return item;
  };
  const sort = element(doc, "menuitem", "", { label: "Sort into a group", tooltiptext: "Place this page in the group it belongs in, by your earlier sorting, the rules and what you work on now" });
  sort.addEventListener("command", () => runSlyb(win, ["organize", "--workspace", space, "--page", page.url], row, null));
  const tab = row._slyTab;
  const action = (label, tooltip, run) => {
    const item = element(doc, "menuitem", "", { label, tooltiptext: tooltip });
    item.addEventListener("command", run);
    return item;
  };
  const ownGroup = row.closest(".sly-card")?.dataset.key?.match(/^group:(.+)$/)?.[1];
  const group = State.groups.find(entry => entry.id === ownGroup) ?? null;
  const drifted = tab && ownGroup && !page.closable && tabUrl(tab) !== page.url;
  const setCurrent = element(doc, "menuitem", "", { label: "Set to current URL", tooltiptext: drifted ? `Store ${tabUrl(tab)} instead` : "" });
  setCurrent.addEventListener("command", () => {
    const address = tabUrl(tab);
    State.changeGroup(ownGroup, group => {
      for (const section of group.sections) {
        for (const entry of section.pages) {
          if (entry.url === page.url) {
            entry.url = address;
            entry.title = tab.label;
          }
        }
      }
    })
      .then(() => {
        tagTab(tab, "sly-page", address);
        renderCards(win);
      })
      .catch(logError);
  });
  const onApps = pinnedApps(win.gZenWorkspaces.activeWorkspace).some(app => app.url === page.url);
  const tabItems = [
    header("Tab"),
    action("Open in new tab", "Open this page in another tab", () => {
      if (!group) {
        win.openTrustedLinkIn(page.url, "tab");
        return;
      }
      const copy = win.gBrowser.addTrustedTab(page.url, { inBackground: false, userContextId: group.login ? loginContext(group.login) : getSpaceContext(win, group.space) });
      if (copy.getAttribute("zen-workspace-id") !== group.space) {
        win.gZenWorkspaces.moveTabToWorkspace(copy, group.space);
      }
      tagTab(copy, "sly-group", group.id);
      win.gBrowser.selectedTab = copy;
    }),
    ...(tab
      ? [
          action("Duplicate tab", "Open a copy of this tab, with its history", () => {
            const copy = win.gBrowser.duplicateTab(tab);
            if (group) {
              copy.addEventListener("SSTabRestoring", () => tagTab(copy, "sly-group", group.id), { once: true });
            }
            win.gBrowser.selectedTab = copy;
          }),
        ]
      : []),
    action("Open in private window", "Open this page in a private window", () => win.openTrustedLinkIn(page.url, "window", { private: true })),
    ...(tab ? [action("Reload without cache", "Reload this page, fetching everything again", () => win.gBrowser.reloadTab(tab, { flags: Ci.nsIWebNavigation.LOAD_FLAGS_BYPASS_CACHE | Ci.nsIWebNavigation.LOAD_FLAGS_BYPASS_PROXY }))] : []),
    action("Clear cache for this page", "Remove what the browser cached for this page's site, keeping its logins", () => {
      const principal = Services.scriptSecurityManager.createContentPrincipal(Services.io.newURI(page.url), {});
      Services.clearData.deleteDataFromPrincipal(principal, true, Ci.nsIClearDataService.CLEAR_ALL_CACHES, { onDataDeleted: () => tab && win.gBrowser.reloadTab(tab) });
    }),
    action(onApps ? "Remove from apps" : "Pin to apps", onApps ? "Take it off the app grid at the top" : "Put it on the app grid at the top", () => pinApp(win, page, !onApps)),
    action("Copy address", page.url, () => Cc["@mozilla.org/widget/clipboardhelper;1"].getService(Ci.nsIClipboardHelper).copyString(page.url)),
    ...(drifted ? [setCurrent] : []),
    ...(tab ? [action("Close tab", ownGroup && !tab.hasAttribute("pending") ? "Unload this tab, keeping it in the group" : "Close this tab", () => (ownGroup ? unloadTab(win, tab).catch(logError) : win.gBrowser.removeTab(tab)))] : []),
    element(doc, "menuseparator"),
  ];
  const putRule = name =>
    State.changeGroup(ownGroup, group => {
      for (const section of group.sections) {
        for (const entry of section.pages) {
          if (entry.rule !== page.rule) {
            continue;
          }
          if (name) {
            entry.rule_name = name;
          } else {
            delete entry.rule;
            delete entry.rule_name;
          }
        }
      }
    }).then(() => renderCards(win));
  const ruleItems =
    ownGroup && page.rule
      ? [
          header(`Rule · ${page.rule_name || "automation"}`),
          action("Change rule…", "Say how the rule that placed this page should change; Claude rewrites it", () =>
            openPromptDialog(win, { title: `Change ${page.rule_name || "the rule"}`, hint: `It placed ${page.title || page.url} here. Say what it should catch instead or as well.`, placeholder: "Such as only the prd hosts, not the docs", action: "Change" }, text =>
              runSlyb(win, ["rule", "update", page.rule, "--ask", text], row, null)
                .then(async done => done && putRule(((await runSlybCommand(["rule", "get"])) ?? []).find(rule => rule.id === page.rule)?.name ?? page.rule_name))
                .catch(logError)
            )
          ),
          action("Remove rule", "Delete the rule that placed this page; the page stays", () =>
            runSlyb(win, ["rule", "delete", page.rule], row, null)
              .then(done => done && putRule(null))
              .catch(logError)
          ),
          remove("Not this page", "Keep the rule but take this page out of the group", ["group"]),
          element(doc, "menuseparator"),
        ]
      : [];
  const items = [
    ...tabItems,
    ...ruleItems,
    header("Assistant"),
    sort,
    element(doc, "menuseparator"),
    ...(ownGroup
      ? [
          header("Group"),
          remove("Remove from group", "Take this page out of the group; an open tab stays open as a loose page", ["group"]),
          remove("Remove and clear everywhere", "Take it out of the group, close its tab, forget it in Claude and delete it from your browsing history", ["group", "close", "claude", "history"]),
          element(doc, "menuseparator"),
        ]
      : []),
    header("This page"),
    remove("Remove from history", "Delete this page from your browsing history", ["history"]),
    remove("Remove from Claude", "Forget this page in what Claude taught the browser, everywhere", ["claude"]),
    remove("Remove from both", "Forget it in Claude and delete it from your browsing history", ["claude", "history"]),
  ];
  const isolate = (label, tooltip, hint, copy) => {
    const item = element(doc, "menuitem", "", { label, tooltiptext: tooltip });
    item.addEventListener("command", () => {
      const name = (URL.parse(page.url)?.hostname ?? "").replace(/^www\./, "");
      openPromptDialog(win, { title: label.replace("…", ""), hint, placeholder: "Such as Claude work", action: "Isolate", value: page.title || name }, text => isolatePage(win, space, row, page, text, copy).catch(logError));
    });
    return item;
  };
  items.push(
    isolate("Isolate with this session…", `Move this page into a new group of its own, with a separate login that starts with your current ${domain} session`, `The page moves into a new group with a separate login of this name. The cookies of ${domain} are copied into it, so you stay signed in there and the two sessions go their own way from now on.`, true),
    isolate("Isolate in its own group…", "Move this page into a new group of its own, with a separate login that starts signed out", "The page moves into a new group with a separate login of this name, so it keeps its own session.", false)
  );
  const live = page.liveChild ? null : State.liveItem(page.url);
  if (!page.liveChild && !live) {
    items.push(element(doc, "menuseparator"), header("Live"), action("Make live", "Listen for what changes on this page: a GitHub repository lists its open pull requests and issues, any other page counts from its tab title", () => runSlyb(win, ["live", "create", page.url], row, null)));
  } else if (live) {
    const every = minutes => {
      const item = action(minutes ? `Every ${minutes} min` : "Only when opened", minutes ? `Sync this live item every ${minutes} minutes` : "Sync this live item only when its page gets focus", () => runSlyb(win, ["live", "update", live.url, "--every", String(minutes)], row, null));
      item.setAttribute("type", "radio");
      item.setAttribute("name", "sly-live-every");
      if ((live.every ?? 0) === minutes) {
        item.setAttribute("checked", "true");
      }
      return item;
    };
    items.push(
      element(doc, "menuseparator"),
      header(`Live · ${live.source === "github" ? "pull requests and issues" : "from the tab title"}`),
      ...(live.source === "github" ? [action("Refresh now", "Read the open pull requests and issues now", () => runSlyb(win, ["live", "sync", live.url], row, null)), ...[1, 5, 15, 60, 0].map(every)] : []),
      ...(live.children?.length ? [action(liveFolded.has(live.url) ? "Show subpages" : "Fold subpages", "Show or hide its items under the page", () => {
        liveFolded.has(live.url) ? liveFolded.delete(live.url) : liveFolded.add(live.url);
        renderCards(win);
      })] : []),
      action("Stop live", "The page stays; it no longer listens or shows a count", () => runSlyb(win, ["live", "delete", live.url], row, null))
    );
  }
  items.push(element(doc, "menuseparator"), header("Site"));
  const clear = element(doc, "menuitem", "", { label: `Clear history of ${domain}…`, tooltiptext: `Delete every visited page of ${domain} and its subdomains` });
  clear.addEventListener("command", () =>
    openWarning(win, row, {
      title: `Clear the history of ${domain}?`,
      text: `Every page of ${domain} and its subdomains is deleted from your history, and leaves the cards built from history. Groups you made keep their pages. This cannot be undone.`,
      action: "Clear history",
      run: () => {
        const rows = [...doc.querySelectorAll(".sly-card-tab")].filter(other => {
          const otherHost = URL.parse(other.dataset.url ?? "")?.host ?? "";
          return otherHost === domain || otherHost.endsWith(`.${domain}`);
        });
        rows.forEach(vanish);
        runSlyb(win, ["history", "delete", "--host", domain], row, null).then(done => done || rows.forEach(other => other.removeAttribute("sly-gone")));
      },
    })
  );
  items.push(clear);
  menu.replaceChildren(...items);
  iconize(menu);
  menu.openPopupAtScreen(event.screenX, event.screenY, true);
}

function vanish(node) {
  node?.setAttribute("sly-gone", "true");
}

const focused = new WeakMap();

function recordFocus(win, flush = false) {
  const now = Date.now();
  const tab = win.gBrowser.selectedTab;
  const url = tab ? tabUrl(tab) : "";
  const active = Services.focus.activeWindow === win && win.document.visibilityState === "visible";
  const devtools = !!(tab && lazy.DevToolsShim.isInitialized() && lazy.DevToolsShim.hasToolboxForTab(tab));
  const current = focused.get(win);
  const same = current && active && current.tab === tab && current.url === url && current.devtools === devtools;
  if (current && (!same || flush)) {
    const title = current.tab.label || current.title;
    if (now - current.start >= 2000) {
      const day = new Date(current.start);
      const pad = value => String(value).padStart(2, "0");
      const file = PathUtils.join(paths.focus, `${day.getFullYear()}-${pad(day.getMonth() + 1)}-${pad(day.getDate())}.jsonl`);
      const line = JSON.stringify({ start: new Date(current.start).toISOString(), end: new Date(now).toISOString(), url: current.url, title, space: current.space, devtools: current.devtools }) + "\n";
      IOUtils.makeDirectory(paths.focus, { ignoreExisting: true })
        .then(() => IOUtils.writeUTF8(file, line, { mode: "appendOrCreate" }))
        .catch(logError);
    }
    focused.delete(win);
  }
  if (same && !flush) {
    current.title = tab.label || current.title;
    return;
  }
  if (active && /^https?:/.test(url)) {
    focused.set(win, { tab, url, title: tab.label, space: tab.getAttribute("zen-workspace-id") ?? "", devtools, start: now });
  }
}

function renderFooter(win) {
  const doc = win.document;
  for (const button of doc.querySelectorAll("#zen-workspaces-button > toolbarbutton[zen-workspace-id]")) {
    const id = button.getAttribute("zen-workspace-id");
    const space = win.gZenWorkspaces.getWorkspaceFromId(id);
    const icon = space?.icon ? "" : workspaceIcon(doc, id) ?? "";
    const shown = icon ? iconSource(icon) ?? icon : "";
    if (button.dataset.slyIcon === shown) {
      continue;
    }
    button.dataset.slyIcon = shown;
    button.querySelector(":scope > .sly-space-dot")?.remove();
    button.toggleAttribute("sly-icon", !!icon);
    if (icon) {
      button.append(iconElement(doc, icon, "sly-space-dot"));
    }
  }
  let name = doc.getElementById("sly-space-name");
  if (!name) {
    name = element(doc, "label", "sly-space-name", { id: "sly-space-name" });
    doc.getElementById("zen-create-new-button")?.before(name);
  }
  name.textContent = win.gZenWorkspaces.getActiveWorkspace()?.name ?? "";
}

function workspaceIcon(doc, id) {
  const domain = State.spaceTies?.[id]?.domains?.[0];
  return domain ? `https://${domain}` : readPref(spaceIconsPref)[id] ?? doc.getElementById(id)?._slyIcon ?? null;
}

function relatesTo(host, domain) {
  if (!host || !domain) {
    return false;
  }
  if (host === domain || host.endsWith(`.${domain}`)) {
    return true;
  }
  const name = domain.split(".")[0];
  return host.split(".").slice(0, -1).includes(name);
}

function domainSpace(url) {
  const host = (URL.parse(url)?.hostname ?? "").replace(/^www\./, "");
  const text = decodeURIComponent(url).toLowerCase();
  for (const [space, tie] of Object.entries(State.spaceTies ?? {})) {
    for (const domain of tie.domains ?? []) {
      if (relatesTo(host, domain) || text.includes(`@${domain}`) || /[?&](domain_hint|hd|whr)=/.test(text) && text.includes(`=${domain}`)) {
        return space;
      }
    }
  }
  const spaces = new Set(State.projects.filter(entry => entry.space && entry.remote && URL.parse(entry.remote)?.hostname === URL.parse(url)?.hostname).map(entry => entry.space));
  return spaces.size === 1 ? [...spaces][0] : null;
}

function arrangeText(title) {
  return { title: `Arrange ${title}`, hint: "Say how to sort or relate what it holds. It comes before the organization rules.", placeholder: "Such as by client, newest work first", action: "Arrange" };
}

const zenIcon = name => `chrome://browser/skin/zen-icons/${name}.svg`;

const menuIcons = [
  [/^Open in new tab/, zenIcon("tab")],
  [/^Duplicate/, zenIcon("edit-copy")],
  [/^Open in private/, zenIcon("window")],
  [/^Reload/, zenIcon("reload")],
  [/^Clear cache/, zenIcon("history")],
  [/^Copy address/, zenIcon("link")],
  [/^Pin to apps/, zenIcon("pin")],
  [/^Remove from apps/, zenIcon("unpin")],
  [/^Open in new tab/, zenIcon("tab")],
  [/^Set to current/, zenIcon("pin")],
  [/^Close tab/, zenIcon("close")],
  [/^(Organize|Sort into a group|Sort them|Sort with)/, zenIcon("sparkles")],
  [/^Find similar/, zenIcon("search-page")],
  [/^Summarize/, zenIcon("info")],
  [/(by prompt|^Arrange)/, zenIcon("algorithm")],
  [/^Rules/, zenIcon("share")],
  [/^Rename/, "chrome://global/skin/icons/edit.svg"],
  [/^Icon/, zenIcon("selectable/image")],
  [/^Domain/, zenIcon("selectable/globe")],
  [/^Theme/, zenIcon("palette")],
  [/^(Remove|Delete)/, zenIcon("trash")],
  [/^Clear history/, zenIcon("history")],
  [/^New (workspace|space)/, zenIcon("plus")],
  [/^(Profile|No profile)/, zenIcon("container-tab")],
  [/^Make a group/, zenIcon("folder")],
  [/^(Isolate|Separate login|Use the workspace login)/, zenIcon("container-tab")],
  [/^Sign out/, zenIcon("history")],
  [/^Build a space/, zenIcon("algorithm")],
  [/^Preview/, zenIcon("selectable/eye")],
];

function iconize(menu) {
  for (const item of menu.querySelectorAll(":scope > menuitem, .sly-menu-row")) {
    if (item.classList.contains("sly-menu-header") || item.hasAttribute("type") || item.hasAttribute("image")) {
      continue;
    }
    const icon = menuIcons.find(([pattern]) => pattern.test(item.getAttribute("label") ?? ""))?.[1];
    if (icon) {
      item.classList.add("menuitem-iconic");
      item.setAttribute("image", icon);
    }
  }
}

function spaceBlocks(win, workspace, cards, render) {
  const doc = win.document;
  const spaces = (State.spaces ?? []).filter(space => space.workspace === workspace.id);
  return spaces.map(space => {
    const mine = cards.filter(card => !card.plain && (card.group?.space_id || spaces[0].id) === space.id);
    if (space.sort !== "manual") {
      let order = groupOrder.get(workspace.id);
      if (!order) {
        const score = group => (group?.used ?? 0) * 0.5 ** ((Date.now() - (Date.parse(group?.used_at ?? "") || 0)) / 604800000);
        order = new Map(cards.filter(card => card.group).sort((a, b) => score(b.group) - score(a.group)).map((card, index) => [card.group.id, index]));
        groupOrder.set(workspace.id, order);
      }
      mine.sort((a, b) => (order.get(a.group?.id) ?? -1) - (order.get(b.group?.id) ?? -1));
    }
    const key = `space:${space.id}`;
    const folded = !!cardState(key).collapsed;
    const block = element(doc, "vbox", "sly-space-block");
    block.dataset.key = key;
    block.dataset.space = space.id;
    block.toggleAttribute("folded", folded);
    block.toggleAttribute("sly-ai", thinking.has(key));
    const head = element(doc, "hbox", "sly-space-head", { align: "center", tooltiptext: folded ? `Expand ${space.name}` : `Collapse ${space.name}` });
    const top = mine.filter(card => card.icon).sort((a, b) => b.weight - a.weight)[0];
    const icon = space.icon || top?.favicon || top?.icon;
    const more = element(doc, "toolbarbutton", "sly-card-more", { tooltiptext: `More for ${space.name}` });
    more.addEventListener("click", event => {
      stopEvent(event);
      openSpaceMenu(win, workspace, space, more);
    });
    head.append(
      icon === top?.favicon && icon ? iconImage(doc, "sly-space-head-icon", icon) : icon ? iconElement(doc, icon, "sly-space-head-icon") : element(doc, "box", "sly-space-head-icon"),
      element(doc, "label", "sly-space-head-name", { value: space.name, crop: "end", flex: "1" }),
      element(doc, "label", "sly-card-count", { value: String(mine.length) }),
      more,
      element(doc, "image", "sly-card-chevron")
    );
    head.addEventListener("click", () => {
      putCardState(key, { collapsed: !folded });
      renderCards(win);
    });
    const body = element(doc, "vbox", "sly-space-body");
    if (!folded) {
      body.append(...shelved(win, mine, render));
    }
    block.append(head, body);
    return block;
  });
}

function sidebarSpaceRows(win, sidebar, workspace, anchor, row, header) {
  const siblings = (State.spaces ?? []).filter(entry => entry.workspace === sidebar.workspace);
  const workspaceName = win.gZenWorkspaces.getWorkspaceFromId(sidebar.workspace)?.name ?? "the workspace";
  const rows = [
    row("Rename…", "Name this space of the sidebar; the workspace keeps its own name", () =>
      openPromptDialog(win, { title: "Rename the space", hint: "The name of this part of the sidebar. The workspace keeps its own name.", placeholder: "Such as Clients", action: "Rename", value: sidebar.name }, text =>
        State.changeSpace(sidebar.id, space => (space.name = text)).then(() => renderCards(win), logError)
      )
    ),
    row("Icon…", "Change the icon of this space", () =>
      openIconPicker(win, { title: sidebar.name, origins: workspace?._slyOrigins ?? [], current: sidebar.icon, anchor, choose: value => State.changeSpace(sidebar.id, space => (space.icon = value)) })
    ),
    row("New space…", "Add another space to this workspace", () =>
      openPromptDialog(win, { title: "New space", hint: "A space holds groups of its own in the sidebar of this workspace.", placeholder: "Such as Clients", action: "Create space" }, text => {
        State.spaces.push({ id: Math.random().toString(16).slice(2, 14), name: text, icon: "", workspace: sidebar.workspace, created_at: new Date().toISOString(), history: [], discarded: [] });
        State.saveGroups().then(() => renderCards(win), logError);
      })
    ),
    header("Order groups"),
  ];
  for (const [value, label] of [["", "Most used first"], ["manual", "As arranged"]]) {
    rows.push(row(label, `Order the groups of ${sidebar.name} ${label.toLowerCase()}`, () => State.changeSpace(sidebar.id, space => (space.sort = value)).then(() => renderCards(win), logError), (sidebar.sort ?? "") === value ? { checked: "true" } : {}));
  }
  const groups = State.groups.filter(group => (group.space_id ? group.space_id === sidebar.id : group.space === sidebar.workspace && siblings[0]?.id === sidebar.id));
  const tabs = browserWindows().flatMap(other => realTabs(other)).filter(tab => groups.some(group => groupOfTab(tab)?.id === group.id));
  rows.push(
    row(
      "Delete space…",
      `Delete this space of the sidebar with its ${groups.length} groups and their pages, and close their ${tabs.length} open tabs. The workspace ${workspaceName} stays`,
      () =>
        openWarning(win, anchor, {
          title: `Delete the space ${sidebar.name}?`,
          text: `Its ${groups.length} ${groups.length === 1 ? "group goes" : "groups go"} with it, with their pages${tabs.length ? `, and their ${tabs.length} open ${tabs.length === 1 ? "tab closes" : "tabs close"}` : ""}. The workspace ${workspaceName} and its other tabs stay as they are. This cannot be undone.`,
          action: "Delete space",
          run: async () => {
            await State.loadGroups();
            const gone = new Set(groups.map(group => group.id));
            State.groups = State.groups.filter(group => !gone.has(group.id));
            State.spaces = State.spaces.filter(entry => entry.id !== sidebar.id);
            await State.saveGroups();
            for (const tab of tabs) {
              tab.ownerGlobal.gBrowser.removeTab(tab);
            }
            renderCards(win);
          },
        }),
      { danger: "true" }
    )
  );
  return rows;
}

async function isolatePage(win, workspace, row, page, name, copy) {
  await State.loadGroups();
  const from = row.closest(".sly-card")?.dataset.key?.match(/^group:(.+)$/)?.[1];
  const source = from ? State.groups.find(group => group.id === from) : null;
  if (copy) {
    const host = URL.parse(page.url)?.hostname ?? "";
    let domain = host;
    try {
      domain = Services.eTLD.getBaseDomainFromHost(host);
    } catch {}
    const context = row._slyTab?.userContextId ?? (source?.login ? loginContext(source.login) : win.gZenWorkspaces.getWorkspaceFromId(workspace)?.containerTabId ?? 0);
    const target = loginContext(name);
    for (const cookie of Services.cookies.getCookiesWithOriginAttributes(JSON.stringify({ userContextId: context }), domain)) {
      Services.cookies.add(cookie.host, cookie.path, cookie.name, cookie.value, cookie.isSecure, cookie.isHttpOnly, cookie.isSession, cookie.expiry, { ...cookie.originAttributes, userContextId: target }, cookie.sameSite, cookie.schemeMap, cookie.isPartitioned);
    }
  }
  const home = source?.space_id ?? State.spaces.find(space => space.workspace === workspace)?.id ?? "";
  for (const section of source?.sections ?? []) {
    section.pages = section.pages.filter(entry => entry.url !== page.url);
  }
  const group = {
    id: Math.random().toString(16).slice(2, 14),
    title: name,
    shelf: source?.shelf ?? "",
    icon: "",
    color: "",
    space: workspace,
    claude: false,
    sort: "relevance",
    space_id: home,
    login: name,
    created_at: new Date().toISOString(),
    sections: [{ id: Math.random().toString(16).slice(2, 14), label: "", kind: "section", pages: [{ url: page.url, title: page.title, source: "you", verified: true, added_at: new Date().toISOString() }] }],
  };
  State.groups.push(group);
  await State.saveGroups();
  State.learn({ action: "kept", url: page.url, title: page.title, group: group.title, section: "" });
  const old = row._slyTab;
  const tab = await postOpenPage(win, page.url, workspace, { userContextId: loginContext(name) });
  tagTab(tab, "sly-page", page.url);
  if (old && old !== tab) {
    win.gBrowser.removeTab(old);
  }
  opened.add(`group:${group.id}`);
  renderCards(win);
}

function setSidebarInk(win, under) {
  const ground = luminance(under);
  const contrast = ink => (Math.max(ground, ink) + 0.05) / (Math.min(ground, ink) + 0.05);
  const ink = contrast(luminance([18, 12, 20])) > contrast(luminance([255, 252, 254])) ? "dark" : "light";
  const color = ink === "dark" ? "rgb(18, 12, 20)" : "rgb(255, 252, 254)";
  win.document.documentElement.setAttribute("sly-sidebar-ink", ink);
  for (const node of [win.document.getElementById("navigator-toolbox"), ...win.document.querySelectorAll("zen-workspace")]) {
    node?.style.setProperty("--toolbox-textcolor", color, "important");
    node?.style.setProperty("--toolbar-color", color, "important");
    node?.style.setProperty("color", color, "important");
  }
}

async function sampleSidebar(win) {
  if (Services.focus.activeWindow !== win || win.document.visibilityState !== "visible") {
    return;
  }
  const box = win.document.getElementById("navigator-toolbox").getBoundingClientRect();
  const region = [win.mozInnerScreenX + box.left + 2, win.mozInnerScreenY + box.top + box.height * 0.4, 2, box.height * 0.35].map(value => String(Math.round(value)));
  const sample = await runSlybCommand(["theme", "sample", `--region=${region.join(",")}`]).catch(() => null);
  if (sample?.rgb && Services.focus.activeWindow === win) {
    win.document.documentElement.style.setProperty("--sly-under", `rgb(${sample.rgb.join(" ")})`);
    setSidebarInk(win, sample.rgb);
    if (sample.rgb.join() !== sidebarBase.join()) {
      sidebarBase = sample.rgb;
      renderCards(win);
    }
  }
}

function landingGroup(win) {
  if (win._slyLand === "") {
    return null;
  }
  return win._slyLand ? State.groups.find(group => group.id === win._slyLand) ?? null : groupOfTab(win.gBrowser.selectedTab);
}

function markLanding(win) {
  const key = `group:${landingGroup(win)?.id}`;
  for (const card of win.document.querySelectorAll(".sly-card[data-key^=\"group:\"]")) {
    card.toggleAttribute("sly-land", card.dataset.key === key);
  }
  for (const workspace of win.document.querySelectorAll("zen-workspace[sly-cards]")) {
    workspace.toggleAttribute("sly-land-loose", !workspace.querySelector(":scope > .sly-cards .sly-card[sly-land]"));
  }
}

function groupOfTab(tab) {
  const own = tab?.getAttribute("sly-group");
  if (own) {
    return State.groups.find(group => group.id === own) ?? null;
  }
  const page = tab?.getAttribute("sly-page");
  return page ? groupOfPage(page, tab.getAttribute("zen-workspace-id")) : null;
}

async function unloadTab(win, tab) {
  if (tab.hasAttribute("pending")) {
    win.gBrowser.removeTab(tab);
    await renderCards(win);
    return;
  }
  if (tab.selected) {
    win.gBrowser.selectedTab = win.gBrowser._findTabToBlurTo(tab, [tab]);
  }
  if (await win.gBrowser.explicitUnloadTabs([tab])) {
    tab.removeAttribute("discarded");
  }
  if (tab.hasAttribute("pending")) {
    tab._slyUnloaded = Date.now();
  }
  await renderCards(win);
}

function groupOfPage(url, space) {
  const holding = State.groups.filter(group => group.sections.some(section => section.pages.some(page => page.url === url)));
  return holding.find(group => group.space === space) ?? holding[0] ?? null;
}

function pageTaken(win, tab, url, space) {
  return realTabs(win).some(other => other !== tab && other.getAttribute("sly-page") === url && other.getAttribute("zen-workspace-id") === space);
}

function tagTab(tab, key, value) {
  for (const name of ["sly-page", "sly-group"]) {
    if (name !== key && tab.hasAttribute(name)) {
      tab.removeAttribute(name);
      lazy.SessionStore.deleteCustomTabValue(tab, name);
    }
  }
  if (key) {
    lazy.SessionStore.setCustomTabValue(tab, key, value);
    tab.setAttribute(key, value);
  }
}

const pinnedPref = "slybrowser.apps.pinned";

function pinnedApps(space) {
  return JSON.parse(Services.prefs.getStringPref(pinnedPref, "[]")).filter(app => app.space === space);
}

function pinApp(win, page, pin, at) {
  const space = win.gZenWorkspaces.activeWorkspace;
  const kept = pinnedApps(space).filter(app => app.url !== page.url);
  if (pin) {
    kept.splice(at ?? kept.length, 0, { url: page.url, title: page.title || page.url, space });
  }
  Services.prefs.setStringPref(pinnedPref, JSON.stringify([...JSON.parse(Services.prefs.getStringPref(pinnedPref, "[]")).filter(app => app.space !== space), ...kept]));
  renderApps(win).catch(logError);
  renderCards(win);
}

function openAppMenu(win, app, event) {
  const doc = win.document;
  let menu = doc.getElementById("sly-app-menu");
  if (!menu) {
    menu = doc.createXULElement("menupopup");
    menu.id = "sly-app-menu";
    doc.getElementById("mainPopupSet").append(menu);
  }
  const item = (label, run) => {
    const entry = element(doc, "menuitem", "", { label });
    entry.addEventListener("command", run);
    return entry;
  };
  menu.replaceChildren(
    item("Open in new tab", () => win.openTrustedLinkIn(app.url, "tab")),
    app.pinned ? item("Remove from apps", () => pinApp(win, app, false)) : item("Pin to apps", () => pinApp(win, app, true))
  );
  iconize(menu);
  menu.openPopupAtScreen(event.screenX, event.screenY, true);
}

function siteOf(url) {
  const host = URL.parse(url)?.hostname ?? "";
  try {
    return Services.eTLD.getBaseDomainFromHost(host);
  } catch {
    return host;
  }
}

function isLoose(tab) {
  const url = tabUrl(tab);
  return (
    /^https?:/.test(url) &&
    !tab.pinned &&
    !tab.hasAttribute("zen-essential") &&
    !tab.hasAttribute("sly-page") &&
    !tab.hasAttribute("sly-group") &&
    !pinnedApps(tab.getAttribute("zen-workspace-id")).some(app => app.url === url) &&
    !State.groups.some(group => group.sections.some(section => section.pages.some(page => page.url === url)))
  );
}

async function groupLoose(win, opener, tab) {
  const from = tabUrl(opener);
  const to = tabUrl(tab);
  if (!opener.isConnected || !isLoose(opener) || !/^https?:/.test(to) || siteOf(from) === siteOf(to)) {
    return;
  }
  const workspace = opener.getAttribute("zen-workspace-id");
  await State.loadGroups();
  let space = State.spaces.find(entry => entry.workspace === workspace && entry.name === "Loose");
  if (!space) {
    space = { id: Math.random().toString(16).slice(2, 14), name: "Loose", icon: "", workspace, created_at: new Date().toISOString(), history: [], discarded: [] };
    State.spaces.push(space);
  }
  const at = new Date().toISOString();
  const group = {
    id: Math.random().toString(16).slice(2, 14),
    title: siteOf(from).split(".")[0].replace(/^./, letter => letter.toUpperCase()),
    shelf: "",
    icon: "",
    color: "",
    space: workspace,
    claude: false,
    sort: "relevance",
    space_id: space.id,
    created_at: at,
    sections: [
      {
        id: Math.random().toString(16).slice(2, 14),
        label: "",
        kind: "section",
        pages: [
          { url: from, title: opener.label, source: "you", verified: true, added_at: at },
          { url: to, title: tab.label, source: "you", verified: true, added_at: at },
        ],
      },
    ],
  };
  State.groups.push(group);
  space.history.push({ at, url: to, title: tab.label, group: group.title, reason: `opened from ${siteOf(from)}` });
  await State.saveGroups();
  for (const [member, url] of [[opener, from], [tab, to]]) {
    tagTab(member, "sly-page", url);
  }
  opened.add(`group:${group.id}`);
  renderCards(win);
}
