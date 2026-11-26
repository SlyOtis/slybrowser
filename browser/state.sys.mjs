const home = PathUtils.join(Services.dirsvc.get("Home", Ci.nsIFile).path, ".slyb");

export const paths = {
  home,
  socket: PathUtils.join(home, "browser.sock"),
  groups: PathUtils.join(home, "groups.json"),
  summaries: PathUtils.join(home, "summaries.json"),
  welcome: PathUtils.join(home, "welcome.json"),
  profile: PathUtils.join(home, "profile.json"),
  settings: PathUtils.join(home, "settings.json"),
  projects: PathUtils.join(home, "projects.json"),
  activity: PathUtils.join(home, "activity.json"),
  focus: PathUtils.join(home, "focus"),
  learned: PathUtils.join(home, "learned.jsonl"),
  icons: PathUtils.join(home, "icons.json"),
  triggers: PathUtils.join(home, "actions", "triggers.json"),
  pages: PathUtils.join(home, "pages.json"),
  spaces: PathUtils.join(home, "spaces.json"),
  live: PathUtils.join(home, "live.json"),
  word: PathUtils.join(home, "word.json"),
  images: PathUtils.join(home, "cache", "images.json"),
  log: PathUtils.join(home, "browser.log"),
};

async function readJSON(path, empty) {
  if (!(await IOUtils.exists(path))) {
    return empty;
  }
  return IOUtils.readJSON(path);
}

export const State = {
  groups: [],
  summaries: [],
  themeColors: null,
  projects: [],
  settings: {},
  live: [],
  triggers: {},
  pages: "",

  async load() {
    this.settings = await readJSON(paths.settings, {});
    await this.loadGroups();
    await this.loadProjects();
    await this.loadLive();
    await this.loadTriggers();
  },

  async loadGroups() {
    const store = await readJSON(paths.groups, { groups: [], spaces: [] });
    this.groups = store.groups ?? [];
    this.spaces = store.spaces ?? [];
    this.summaries = (await readJSON(paths.summaries, { summaries: [] })).summaries ?? [];
  },

  async loadLive() {
    this.live = (await readJSON(paths.live, { items: [] })).items ?? [];
  },

  async loadTriggers() {
    this.triggers = (await readJSON(paths.triggers, { triggers: {} })).triggers ?? {};
  },

  liveItem(url) {
    const bare = address => (address ?? "").replace(/\/+$/, "");
    return this.live.find(item => bare(item.url) === bare(url)) ?? null;
  },

  async saveSummaries() {
    await IOUtils.writeJSON(paths.summaries, { summaries: this.summaries }, { tmpPath: `${paths.summaries}.tmp` });
  },

  async getWord() {
    const today = new Date().toLocaleDateString("sv");
    const held = await readJSON(paths.word, null);
    if (held?.date === today) {
      return held;
    }
    const win = Services.wm.getMostRecentWindow("navigator:browser");
    if (!win) {
      return held;
    }
    try {
      const response = await win.fetch("https://www.merriam-webster.com/wotd/feed/rss2", { credentials: "omit", signal: win.AbortSignal.timeout(6000) });
      const feed = new win.DOMParser().parseFromString(await response.text(), "text/xml");
      const item = feed.querySelector("item");
      const word = item?.querySelector("title")?.textContent?.trim();
      if (!word) {
        return held;
      }
      const body = new win.DOMParser().parseFromString(item.querySelector("description")?.textContent ?? "", "text/html");
      const text = body.body.textContent ?? "";
      const meaning = [...body.querySelectorAll("p")].map(p => p.textContent.replace(/\s+/g, " ").trim()).find(line => line && !line.startsWith("Merriam") && !line.startsWith("//") && !line.startsWith(word) && line.toLowerCase().includes(word.toLowerCase()) && !line.includes("See the entry")) ?? "";
      const found = {
        date: today,
        word,
        pronunciation: text.match(/\\([^\\]+)\\/)?.[1] ?? "",
        part: body.querySelector("strong + em, em")?.textContent?.trim() ?? "",
        meaning,
        url: item.querySelector("link")?.textContent?.trim() ?? "https://www.merriam-webster.com/word-of-the-day",
        source: "Merriam-Webster",
      };
      await IOUtils.writeJSON(paths.word, found, { tmpPath: `${paths.word}.tmp` });
      return found;
    } catch (error) {
      logError(error);
      return held;
    }
  },

  async getMoreImages() {
    const win = Services.wm.getMostRecentWindow("navigator:browser");
    if (!win) {
      return [];
    }
    const welcome = await readJSON(paths.welcome, { journey: [] });
    const pages = await readJSON(paths.pages, {});
    const cache = await readJSON(paths.images, {});
    const { PlacesUtils } = ChromeUtils.importESModule("resource://gre/modules/PlacesUtils.sys.mjs");
    const db = await PlacesUtils.promiseDBConnection();
    const since = (Date.now() - 3 * 86400000) * 1000;
    const rows = await db.executeCached("SELECT url FROM moz_places WHERE last_visit_date > :since AND substr(url, 1, 8) = 'https://' ORDER BY visit_count DESC, last_visit_date DESC LIMIT 160", { since });
    const urls = [...new Set([...(welcome.journey ?? []).flatMap(entry => [entry.url, ...(entry.trail ?? []).map(step => step.url)]), ...rows.map(row => row.getResultByName("url"))].filter(url => /^https:\/\//.test(url ?? "")))];
    const missing = urls.filter(url => !(url in cache) && !pages[url]?.image).slice(0, 60);
    const absolute = (value, base) => {
      try {
        const found = new URL(value, base).href;
        return /^https:\/\//.test(found) ? found : null;
      } catch {
        return null;
      }
    };
    const read = async url => {
      try {
        const response = await win.fetch(url, { credentials: "omit", redirect: "follow", signal: win.AbortSignal.timeout(5000) });
        if (!response.ok || !/html/.test(response.headers.get("content-type") ?? "")) {
          return null;
        }
        const doc = new win.DOMParser().parseFromString((await response.text()).slice(0, 400000), "text/html");
        const meta = doc.querySelector('meta[property="og:image"], meta[property="og:image:url"], meta[name="twitter:image"], meta[name="twitter:image:src"], link[rel="image_src"]');
        const value = meta?.getAttribute("content") ?? meta?.getAttribute("href") ?? [...doc.querySelectorAll("img[src]")].find(img => Number(img.getAttribute("width") ?? 0) >= 200 || /hero|cover|banner|feature/i.test(img.className))?.getAttribute("src");
        return value ? absolute(value, response.url || url) : null;
      } catch {
        return null;
      }
    };
    for (let start = 0; start < missing.length; start += 8) {
      const batch = missing.slice(start, start + 8);
      const found = await Promise.all(batch.map(read));
      batch.forEach((url, index) => (cache[url] = found[index]));
    }
    await IOUtils.makeDirectory(PathUtils.parent(paths.images), { ignoreExisting: true });
    await IOUtils.writeJSON(paths.images, cache, { tmpPath: `${paths.images}.tmp` });
    return [...new Set(urls.map(url => pages[url]?.image ?? cache[url]).filter(Boolean))];
  },

  async getWelcome() {
    const welcome = await readJSON(paths.welcome, { taglines: [], journey: [] });
    const profile = await readJSON(paths.profile, {});
    const taglines = welcome.taglines ?? [];
    const artDir = PathUtils.join(home, "browser", "art");
    const art = (await IOUtils.exists(artDir)) ? (await IOUtils.getChildren(artDir)).filter(path => /\.(webp|png|jpe?g)$/i.test(path)).map(path => `art/${PathUtils.filename(path)}`) : [];
    const suggestions = welcome.suggestions ?? { groups: [], spaces: [], rules: [] };
    const pages = await readJSON(paths.pages, {});
    const hostImages = new Map();
    for (const page of Object.values(pages)) {
      const host = URL.parse(page.url ?? "")?.host;
      if (host && /^https:\/\//.test(page.image ?? "") && !hostImages.has(host)) {
        hostImages.set(host, page.image);
      }
    }
    const imageOf = url => {
      const image = pages[url]?.image ?? pages[url?.replace(/\/+$/, "")]?.image;
      if (/^https:\/\//.test(image ?? "")) {
        return image;
      }
      const repo = (url ?? "").match(/^https:\/\/github\.com\/([^\/?#]+\/[^\/?#]+)/)?.[1];
      return repo ? `https://opengraph.githubassets.com/1/${repo}` : hostImages.get(URL.parse(url ?? "")?.host) ?? null;
    };
    const journeyImages = (welcome.journey ?? []).flatMap(entry => [entry.url, ...(entry.trail ?? []).map(step => step.url)]).map(imageOf);
    const recentImages = Object.values(pages)
      .filter(page => /^https:\/\//.test(page.image ?? ""))
      .sort((a, b) => (b.last_seen ?? "").localeCompare(a.last_seen ?? ""))
      .map(page => page.image);
    const images = [...new Set([...journeyImages, ...recentImages].filter(Boolean))].slice(0, 72);
    return {
      art,
      images,
      word: await this.getWord(),
      clock: profile.clock || "24h",
      theme: this.themeColors ?? [],
      suggestions,
      name: profile.name ?? "",
      tagline: taglines.length ? taglines[Math.floor(Math.random() * taglines.length)] : "",
      journey: welcome.journey ?? [],
      since: welcome.since ?? null,
      journey_start: profile.journey_start || "00:00",
      refreshed_at: welcome.refreshed_at ?? null,
    };
  },

  async saveGroups() {
    await IOUtils.writeJSON(paths.groups, { groups: this.groups, spaces: this.spaces }, { tmpPath: `${paths.groups}.tmp` });
  },

  async changeGroup(id, change) {
    await this.loadGroups();
    const group = this.groups.find(entry => entry.id === id);
    if (!group) {
      return null;
    }
    change(group);
    await this.saveGroups();
    return group;
  },

  notePage(data, workspace) {
    if (!this.pages) {
      this.pages = {};
      this.pagesLoaded = readJSON(paths.pages, {}).then(saved => (this.pages = { ...saved, ...this.pages }));
    }
    const key = data.url.replace(/#.*$/, "");
    const now = new Date().toISOString();
    const known = this.pages[key];
    this.pages[key] = { ...known, ...Object.fromEntries(Object.entries(data).filter(([, value]) => value)), url: key, workspace: workspace || known?.workspace || "", first_seen: known?.first_seen ?? now, last_seen: now, seen: (known?.seen ?? 0) + 1 };
    lazyRun.clearTimeout(this.pagesSaving);
    this.pagesSaving = lazyRun.setTimeout(() => {
      this.pagesLoaded
        .then(() => IOUtils.writeJSON(paths.pages, this.pages, { tmpPath: `${paths.pages}.tmp` }))
        .catch(logError);
    }, 5000);
  },

  noteGroupUse(id) {
    this.groupUses ??= new Map();
    this.groupUses.set(id, (this.groupUses.get(id) ?? 0) + 1);
    lazyRun.clearTimeout(this.usesSaving);
    this.usesSaving = lazyRun.setTimeout(async () => {
      const uses = this.groupUses;
      this.groupUses = new Map();
      await this.loadGroups();
      const at = new Date().toISOString();
      for (const group of this.groups) {
        if (uses.has(group.id)) {
          group.used = (group.used ?? 0) + uses.get(group.id);
          group.used_at = at;
        }
      }
      await this.saveGroups();
    }, 8000);
  },

  async changeSpace(id, change) {
    await this.loadGroups();
    const space = this.spaces.find(entry => entry.id === id);
    if (!space) {
      return null;
    }
    change(space);
    await this.saveGroups();
    return space;
  },

  async loadProjects() {
    const index = await readJSON(paths.projects, { projects: [] });
    this.projectsSyncedAt = index.synced_at ?? "";
    this.spaceTies = (await readJSON(paths.spaces, { spaces: {} })).spaces ?? {};
    this.projects = (index.projects ?? []).map(project => ({
      ...project,
      prefixes: [...(project.prefixes ?? [])].sort((a, b) => b.length - a.length),
    }));
  },

  async learn(entry) {
    const line = JSON.stringify({ at: new Date().toISOString(), ...entry }) + "\n";
    await IOUtils.writeUTF8(paths.learned, line, { mode: "appendOrCreate" });
  },

  projectOf(url) {
    const page = url.toLowerCase();
    let best = null;
    let length = 0;
    for (const project of this.projects) {
      for (const prefix of project.prefixes) {
        if (prefix.length > length && page.startsWith(prefix) && "/?#".includes(page[prefix.length] ?? "/")) {
          best = project.id;
          length = prefix.length;
        }
      }
    }
    return best;
  },

  projectName(id) {
    return this.projects.find(project => project.id === id)?.name ?? id;
  },
};

export function logError(error) {
  const line = `${new Date().toISOString()} ${error?.message ?? error}\n${error?.stack ?? ""}\n`;
  IOUtils.writeUTF8(paths.log, line, { mode: "appendOrCreate" }).catch(Cu.reportError);
}

const lazyRun = {};
ChromeUtils.defineESModuleGetters(lazyRun, {
  Subprocess: "resource://gre/modules/Subprocess.sys.mjs",
  setTimeout: "resource://gre/modules/Timer.sys.mjs",
  clearTimeout: "resource://gre/modules/Timer.sys.mjs",
});

async function readPipe(pipe) {
  let text = "";
  for (let chunk = await pipe.readString(); chunk; chunk = await pipe.readString()) {
    text += chunk;
  }
  return text;
}

export async function runSlyb(args) {
  const slyb = State.settings.slyb;
  if (!slyb) {
    throw new Error("slyb is not set up: run slyb install");
  }
  const proc = await lazyRun.Subprocess.call({
    command: slyb,
    arguments: [...args, "--json"],
    environmentAppend: true,
    environment: { NO_COLOR: "1" },
    stderr: "pipe",
  });
  const [output, errors, { exitCode }] = await Promise.all([readPipe(proc.stdout), readPipe(proc.stderr), proc.wait()]);
  if (exitCode !== 0) {
    const lines = errors.trim().split("\n");
    throw new Error(lines.find(line => line.startsWith("slyb: "))?.slice(6) ?? lines.pop() ?? `slyb exited ${exitCode}`);
  }
  return JSON.parse(output || "null");
}
