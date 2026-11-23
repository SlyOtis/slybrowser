const $ = id => document.getElementById(id);
const svgNS = "http://www.w3.org/2000/svg";
let clock = "24h";

function partOfDay(date) {
  const value = date.getHours();
  if (value < 5) return "The night is yours";
  if (value < 12) return "Good morning";
  if (value < 17) return "Good afternoon";
  if (value < 22) return "Good evening";
  return "Good night";
}

function when(value) {
  if (!value) return "";
  const date = new Date(value);
  const time = date.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: clock === "12h" });
  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  if (date.toDateString() === today.toDateString()) return time;
  if (date.toDateString() === yesterday.toDateString()) return `Yesterday ${time}`;
  return `${date.toLocaleDateString(undefined, { day: "numeric", month: "short" })} ${time}`;
}

function host(url) {
  try {
    return new URL(url).host.replace(/^www\./, "");
  } catch {
    return "";
  }
}

function favicon(url, className) {
  const tile = document.createElement("span");
  tile.className = className;
  const image = document.createElement("img");
  try {
    image.src = `${new URL(url).origin}/favicon.ico`;
  } catch {}
  image.alt = "";
  image.addEventListener("error", () => (image.style.visibility = "hidden"), { once: true });
  tile.append(image);
  return tile;
}

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function words(target, text) {
  target.replaceChildren(
    ...text.split(" ").map((word, index) => {
      const span = el("span", "word", word);
      span.style.animationDelay = `${120 + index * 90}ms`;
      return span;
    })
  );
}

function type(target, text, delay) {
  let index = 0;
  setTimeout(function step() {
    target.textContent = text.slice(0, ++index);
    if (index < text.length) setTimeout(step, 24 + Math.random() * 34);
    else document.body.classList.add("typed");
  }, delay);
}

const icons = new Map();

function icon(name) {
  const node = el("span", "icon");
  if (!icons.has(name)) {
    icons.set(
      name,
      fetch(`icons/${name}.svg`)
        .then(response => response.text())
        .then(text => text.replaceAll("context-fill", "currentColor"))
    );
  }
  icons.get(name).then(text => (node.innerHTML = text));
  return node;
}

function popup() {
  let tip = $("tip");
  if (!tip) {
    tip = el("div", "tip");
    tip.id = "tip";
    document.body.append(tip);
  }
  return tip;
}

function hint(node, text) {
  node.dataset.tip = text;
  node.setAttribute("aria-label", text.split("\n")[0]);
  node.addEventListener("pointerenter", () => {
    const tip = popup();
    tip.textContent = node.dataset.tip;
    const box = node.getBoundingClientRect();
    tip.classList.add("shown");
    const left = Math.min(innerWidth - tip.offsetWidth - 12, Math.max(12, box.left + box.width / 2 - tip.offsetWidth / 2));
    const above = box.top - tip.offsetHeight - 10;
    tip.style.left = `${left}px`;
    tip.style.top = `${above > 8 ? above : box.bottom + 10}px`;
  });
  node.addEventListener("pointerleave", () => popup().classList.remove("shown"));
}

function action(name, title, run, className = "") {
  const node = el("button", `act ${className}`.trim());
  node.append(icon(name));
  hint(node, title);
  node.addEventListener("click", async event => {
    event.preventDefault();
    event.stopPropagation();
    node.disabled = true;
    node.classList.add("busy");
    try {
      const done = await run();
      node.classList.add("done");
      node.dataset.tip = done ?? `${title.split("\n")[0]}: done`;
    } catch (error) {
      node.disabled = false;
      node.dataset.tip = `Failed: ${error.message ?? error}`;
    } finally {
      node.classList.remove("busy");
    }
  });
  return node;
}

function linkActions(url, text, plan) {
  const [, groupIndex, group] = plan.group ?? [];
  const [, ruleIndex, rule] = plan.rule ?? [];
  return [
    action("open-new", `Open in a new tab\n${url}`, async () => {
      window.open(url, "_blank");
      return "Opened";
    }),
    action("copy", `Copy the address\n${url}`, async () => {
      await navigator.clipboard.writeText(url);
      return "Copied";
    }),
    group
      ? action("folder-plus", `Add to the group ${group.title}\n${group.reason}${group.applied ? "" : `\nMakes the group first, with ${group.pages?.length ?? 0} pages${group.shelf ? `, on ${group.shelf}` : ""}`}`, async () => {
          await browser.slybrowser.applySuggestion("welcome", "group", groupIndex, url);
          group.applied = true;
          return `Added to ${group.title}`;
        }, "suggested group")
      : action("group", `Group like this\nThe assistant builds a group of the pages like ${host(url)}`, async () => {
          const result = await browser.slybrowser.askAssistant("group", `${text}: pages like ${url}`);
          return result.summary || "Grouped";
        }, "assistant"),
    rule
      ? action("flow", `Add the rule ${rule.name}\n${rule.reason}\nPlaces pages in ${rule.space}\n\n${rule.script}`, async () => {
          await browser.slybrowser.applySuggestion("welcome", "rule", ruleIndex);
          rule.applied = true;
          return `Added ${rule.name}`;
        }, "suggested rule")
      : action("flow", `Rule for ${host(url)}\nThe assistant writes a rule that places pages of ${host(url)} opened from outside the browser`, async () => {
          const result = await browser.slybrowser.askAssistant("rule", `${text}: pages like ${url}`);
          return result.summary || "Rule added";
        }, "assistant"),
  ];
}

const suggestionLook = {
  group: ["folder-plus", entry => `Make the group ${entry.title}\n${entry.reason}\n${entry.pages?.length ?? 0} pages${entry.shelf ? `, on ${entry.shelf}` : ""}`],
  space: ["house-add", entry => `Make the space ${entry.name}\n${entry.reason}\nColor ${entry.color}`],
  rule: ["flow", entry => `Add the rule ${entry.name}\n${entry.reason}\nPlaces pages in ${entry.space}\n\n${entry.script}`],
};

function allSuggestions(suggestions) {
  return [
    ...suggestions.groups.map((entry, index) => ["group", index, entry]),
    ...suggestions.spaces.map((entry, index) => ["space", index, entry]),
    ...suggestions.rules.map((entry, index) => ["rule", index, entry]),
  ];
}

function suggestionAction([kind, index, entry]) {
  const [name, title] = suggestionLook[kind];
  const node = action(name, title(entry), async () => {
    await browser.slybrowser.applySuggestion("welcome", kind, index);
    entry.applied = true;
    return "Done";
  }, `suggested ${kind}`);
  node.disabled = !!entry.applied;
  return node;
}

function pageLink(url, title, className) {
  const link = el("a", `link ${className}`);
  link.href = url;
  link.append(favicon(url, className === "main" ? "tile" : "leaf-icon"));
  const text = el("span", "link-text");
  text.append(el("span", "link-title", title || host(url)), el("span", "meta", host(url)));
  link.append(text);
  return link;
}

function linkRow(link, tools) {
  const row = el("div", "row");
  const box = el("span", "tools");
  box.append(...tools);
  row.append(link, box);
  return row;
}

const ways = { from: "You came here from it", sub: "A page under it you visited", to: "You went on to it", seen: "You had it in front of you", commit: "A change you made", search: "Claude searched this; it links to the change it led to", pr: "A pull request of yours" };

function duration(minutes) {
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  return minutes % 60 ? `${hours} h ${minutes % 60} min` : `${hours} h`;
}

function factsLine(facts, work) {
  if (!facts && !work) return null;
  const parts = [];
  if (work) {
    if (work.commits.length) parts.push(`${work.commits.length} ${work.commits.length === 1 ? "commit" : "commits"}`, `+${work.added} −${work.removed}`, `${work.files} files`);
    if (work.sessions) parts.push(`${work.sessions} Claude ${work.sessions === 1 ? "session" : "sessions"}`);
    if (work.searches.length) parts.push(`${work.searches.length} ${work.searches.length === 1 ? "search" : "searches"}`);
    if (work.pulls.length) parts.push(`${work.pulls.length} pull ${work.pulls.length === 1 ? "request" : "requests"}`);
  }
  facts ??= {};
  if (facts.minutes) parts.push(`${duration(facts.minutes)} in front of you`);
  if (facts.devtools_minutes) parts.push(`dev tools ${duration(facts.devtools_minutes)}`);
  if (facts.visits) parts.push(`${facts.visits} ${facts.visits === 1 ? "visit" : "visits"}`);
  if (facts.pages > 1) parts.push(`${facts.pages} pages`);
  if (facts.first && facts.last && facts.first !== facts.last) parts.push(`${when(facts.first)} to ${when(facts.last)}`);
  if (!parts.length) return null;
  const line = el("p", "facts");
  line.append(...parts.map(part => el("span", "fact", part)));
  if (facts.top?.length) hint(line, `Most of it:\n${facts.top.join("\n")}`);
  if (work?.commits.length) hint(line, `Changes:\n${work.commits.slice(-6).reverse().map(commit => `${commit.subject} (+${commit.added} −${commit.removed})`).join("\n")}`);
  return line;
}

function leaf(step, plan) {
  const item = el("div", `leaf ${step.way}`);
  const link = pageLink(step.url, step.title, "small");
  link.querySelector(".meta").textContent = `${when(step.visited_at)} · ${host(step.url)}`;
  hint(link, `${ways[step.way] ?? ""}\n${step.url}`);
  item.append(el("span", "bud"), linkRow(link, linkActions(step.url, step.title || host(step.url), plan)));
  return item;
}

function node(entry, index, suggestions) {
  const side = index % 2 ? "right" : "left";
  const item = el("article", `node ${side}`);
  item.style.gridRow = String(index + 1);
  const head = el("div", "item");
  const link = pageLink(entry.url, entry.title, "main");
  hint(link, entry.url);
  const mine = allSuggestions(suggestions).filter(([, , suggestion]) => (suggestion.point ?? -1) === index);
  const plan = { group: mine.find(([kind]) => kind === "group"), rule: mine.find(([kind]) => kind === "rule") };
  const tools = [...linkActions(entry.url, entry.text, plan), ...mine.filter(suggestion => suggestion !== plan.group && suggestion !== plan.rule).map(suggestionAction)];
  head.append(...[el("strong", "headline", entry.text), linkRow(link, tools), factsLine(entry.facts, entry.work), el("p", "detail", entry.detail ?? "")].filter(Boolean));
  item.append(head);
  const trail = entry.trail ?? [];
  if (trail.length) {
    const leaves = el("div", "leaves");
    const order = { pr: 0, commit: 1, search: 2, from: 3, seen: 4, sub: 5, to: 6 };
    leaves.append(...[...trail].sort((a, b) => order[a.way] - order[b.way]).map(step => leaf(step, plan)));
    item.append(leaves);
  }
  const stamp = el("time", "stamp", when(entry.visited_at));
  stamp.style.gridRow = String(index + 1);
  return [item, stamp];
}

function rootActions(suggestions, since) {
  const waiting = () => allSuggestions(suggestions).filter(([, , entry]) => !entry.applied);
  const applyAll = kinds => async () => {
    let done = 0;
    for (const suggestion of waiting().filter(([kind]) => kinds.includes(kind))) {
      await browser.slybrowser.applySuggestion("welcome", suggestion[0], suggestion[1]);
      suggestion[2].applied = true;
      done++;
    }
    document.querySelectorAll(".act.suggested").forEach(button => (button.disabled = true));
    return `${done} done`;
  };
  const count = kind => waiting().filter(([entry]) => entry === kind).length;
  const names = { group: entry => `Group ${entry.title}`, space: entry => `Space ${entry.name}`, rule: entry => `Rule ${entry.name} for ${entry.space}` };
  const list = (heading, kinds) => [heading, ...waiting().filter(([kind]) => kinds.includes(kind)).map(([kind, , entry]) => `· ${names[kind](entry)}`)].join("\n");
  const labeled = (name, label, title, run) => {
    const node = action(name, title, run, "root");
    node.append(el("span", "", label));
    return node;
  };
  const actions = [
    labeled("sparkles", "Apply every suggestion", list("Make all of these at once", ["group", "space", "rule"]), applyAll(["group", "space", "rule"])),
    labeled("folder-plus", `Make all groups (${count("group")})`, list("Make these groups", ["group"]), applyAll(["group"])),
    labeled("house-add", `Make all spaces (${count("space")})`, list("Make these spaces", ["space"]), applyAll(["space"])),
    labeled("flow", `Add all rules (${count("rule")})`, list("Add these rules", ["rule"]), applyAll(["rule"])),
    labeled("refresh", "Grow a new journey", "Read the latest browsing and grow the tree again", async () => {
      await browser.slybrowser.refreshWelcome(since ?? undefined);
      location.reload();
    }),
  ];
  const loose = allSuggestions(suggestions).filter(([, , entry]) => (entry.point ?? -1) < 0).map(suggestionAction);
  return [...actions, ...loose];
}

function jitter(seed) {
  const value = Math.sin(seed * 12.9898) * 43758.5453;
  return value - Math.floor(value);
}

function box(element, frame) {
  const a = element.getBoundingClientRect();
  const b = frame.getBoundingClientRect();
  return { left: a.left - b.left, right: a.right - b.left, top: a.top - b.top, bottom: a.bottom - b.top, x: a.left + a.width / 2 - b.left, y: a.top + a.height / 2 - b.top };
}

function path(d, className, width, delay, duration) {
  const line = document.createElementNS(svgNS, "path");
  line.setAttribute("d", d);
  line.setAttribute("class", className);
  line.style.strokeWidth = `${width}`;
  line.style.animationDelay = `${delay}ms`;
  line.style.animationDuration = `${duration}ms`;
  return line;
}

const speed = 0.5;
let grown = false;

function cubic(p0, c1, c2, p3, steps) {
  const points = [];
  for (let index = 0; index <= steps; index++) {
    const t = index / steps;
    const u = 1 - t;
    points.push({
      x: u * u * u * p0.x + 3 * u * u * t * c1.x + 3 * u * t * t * c2.x + t * t * t * p3.x,
      y: u * u * u * p0.y + 3 * u * u * t * c1.y + 3 * u * t * t * c2.y + t * t * t * p3.y,
    });
  }
  return points;
}

function smooth(knots, steps) {
  const points = [knots[0]];
  for (let index = 0; index < knots.length - 1; index++) {
    const [p0, p1, p2, p3] = [knots[index - 1] ?? knots[index], knots[index], knots[index + 1], knots[index + 2] ?? knots[index + 1]];
    const c1 = { x: p1.x + (p2.x - p0.x) / 6, y: p1.y + (p2.y - p0.y) / 6 };
    const c2 = { x: p2.x - (p3.x - p1.x) / 6, y: p2.y - (p3.y - p1.y) / 6 };
    points.push(...cubic(p1, c1, c2, p2, steps).slice(1));
  }
  return points;
}

function line(points) {
  return points.map((point, index) => `${index ? "L" : "M"} ${point.x.toFixed(1)} ${point.y.toFixed(1)}`).join(" ");
}

function tapered(points, from, to) {
  const left = [];
  const right = [];
  points.forEach((point, index) => {
    const next = points[Math.min(points.length - 1, index + 1)];
    const before = points[Math.max(0, index - 1)];
    const dx = next.x - before.x;
    const dy = next.y - before.y;
    const length = Math.hypot(dx, dy) || 1;
    const half = (from + (to - from) * (index / (points.length - 1))) / 2;
    left.push({ x: point.x - (dy / length) * half, y: point.y + (dx / length) * half });
    right.push({ x: point.x + (dy / length) * half, y: point.y - (dx / length) * half });
  });
  return `${line(left)} ${line(right.reverse()).replace(/^M/, "L")} Z`;
}

function wobble(points, amount, seed) {
  const waves = 1 + jitter(seed + 0.5) * 2;
  const phase = jitter(seed + 0.7) * Math.PI * 2;
  return points.map((point, index) => {
    const t = index / (points.length - 1);
    const next = points[Math.min(points.length - 1, index + 1)];
    const before = points[Math.max(0, index - 1)];
    const length = Math.hypot(next.x - before.x, next.y - before.y) || 1;
    const push = Math.sin(t * Math.PI * waves + phase) * amount * Math.sin(t * Math.PI);
    return { x: point.x - ((next.y - before.y) / length) * push, y: point.y + ((next.x - before.x) / length) * push };
  });
}

function twiglets(points, count, seed, width, size, kind, delay, defs) {
  const shapes = [];
  for (let index = 0; index < count; index++) {
    const at = Math.round((0.25 + jitter(seed + index * 3) * 0.6) * (points.length - 1));
    const base = points[at];
    const next = points[Math.min(points.length - 1, at + 1)];
    const heading = Math.atan2(next.y - base.y, next.x - base.x) + (index % 2 ? 1 : -1) * (0.5 + jitter(seed + index) * 0.6);
    const length = size * (0.6 + jitter(seed + index * 5) * 0.8);
    const tip = { x: base.x + Math.cos(heading) * length, y: base.y + Math.sin(heading) * length - length * 0.3 };
    const bend = { x: base.x + Math.cos(heading) * length * 0.5, y: base.y + Math.sin(heading) * length * 0.5 + (jitter(seed + index * 7) - 0.5) * length * 0.4 };
    shapes.push(wood(cubic(base, bend, bend, tip, 14), width, 0.3, kind, delay === null ? 0 : delay + index * 80, delay === null ? 0 : 380, defs));
  }
  return shapes;
}

let masks = 0;

function wood(points, from, to, className, delay, duration, defs) {
  const shape = document.createElementNS(svgNS, "path");
  shape.setAttribute("d", tapered(points, from, to));
  shape.setAttribute("class", `wood ${className}`);
  if (duration) {
    const mask = document.createElementNS(svgNS, "mask");
    mask.id = `grow${masks++}`;
    mask.setAttribute("maskUnits", "userSpaceOnUse");
    const reveal = path(line(points), "reveal", from * 2 + 12, delay, duration);
    reveal.setAttribute("pathLength", "1");
    mask.append(reveal);
    defs.append(mask);
    shape.setAttribute("mask", `url(#${mask.id})`);
  }
  return shape;
}

function drawTree(animate) {
  const tree = $("tree");
  const svg = $("branches");
  svg.setAttribute("width", "0");
  svg.setAttribute("height", "0");
  tree.querySelectorAll(".stamp").forEach(stamp => (stamp.style.translate = ""));
  const grow = (delay, duration) => (animate ? [delay, duration] : [0, 0]);
  const width = tree.clientWidth;
  const height = tree.scrollHeight;
  svg.setAttribute("width", width);
  svg.setAttribute("height", height);
  const center = width / 2;
  const reach = Math.min(width * 0.16, 150);
  const defs = document.createElementNS(svgNS, "defs");
  const clip = document.createElementNS(svgNS, "clipPath");
  clip.id = "growth";
  const tip = document.createElementNS(svgNS, "rect");
  tip.setAttribute("x", "0");
  tip.setAttribute("width", `${width}`);
  tip.setAttribute("y", animate ? `${height + 40}` : "0");
  tip.setAttribute("height", animate ? "0" : `${height + 40}`);
  clip.append(tip);
  defs.append(clip);
  const nodes = [...tree.querySelectorAll(".node")]
    .map(item => ({ item, left: item.classList.contains("left"), tile: box(item.querySelector(".link.main .tile"), tree) }))
    .sort((a, b) => b.tile.y - a.tile.y);
  const knots = [{ x: center, y: height + 30 }, { x: center, y: height - 20 }];
  nodes.forEach(({ left, tile }, index) => {
    const lean = (left ? 1 : -1) * reach * (0.45 + jitter(index * 3 + 1) * 0.55);
    const below = index ? nodes[index - 1].tile.y : height;
    knots.push({ x: center + (jitter(index * 7) - 0.5) * reach * 0.5, y: (below + tile.y) / 2 + 40 });
    knots.push({ x: center + lean, y: tile.y + 30 });
  });
  knots.push({ x: center + (jitter(91) - 0.5) * 30, y: 0 });
  const trunk = smooth(knots.sort((a, b) => b.y - a.y), 16);
  const along = y => trunk.reduce((best, point) => (Math.abs(point.y - y) < Math.abs(best.y - y) ? point : best), trunk[0]);
  const shapes = [];
  const stem = wood(trunk, 18, 3, "trunk", 0, 0, defs);
  stem.setAttribute("clip-path", "url(#growth)");
  shapes.push(stem);
  const stops = [];
  let time = 500;
  let previous = height;
  nodes.forEach(({ item, left, tile }, order) => {
    const start = along(tile.y + 30);
    const end = { x: left ? tile.right + 6 : tile.left - 6, y: tile.y };
    const dx = end.x - start.x;
    time += Math.max(250, (previous - start.y) / speed);
    stops.push({ y: start.y, at: time });
    const stamp = tree.querySelector(`.stamp[style*="grid-row: ${item.style.gridRow};"]`);
    if (stamp) {
      stamp.style.setProperty("--delay", `${time}ms`);
      const at = box(stamp, tree);
      stamp.style.translate = `${(start.x - at.x).toFixed(1)}px ${(start.y - at.y - 18).toFixed(1)}px`;
    }
    item.style.setProperty("--delay", `${time + 350}ms`);
    const sag = (jitter(order * 3 + 5) - 0.3) * 60;
    const limb = wobble(cubic(start, { x: start.x + dx * (0.2 + jitter(order) * 0.25), y: start.y + sag }, { x: start.x + dx * (0.6 + jitter(order + 9) * 0.25), y: end.y + 10 + jitter(order * 5) * 40 }, end, 48), 3 + jitter(order * 2) * 5, order);
    shapes.push(wood(limb, Math.min((18 - 15 * ((height - start.y) / height)) * 0.8, 6 + jitter(order) * 4), 1.4, "limb", ...grow(time, 650), defs));
    shapes.push(...twiglets(limb, 2 + Math.floor(jitter(order * 11) * 3), order * 17, 4, 34, "limb", animate ? time + 300 : null, defs));
    const buds = [...item.querySelectorAll(".bud")];
    buds.forEach((bud, index) => {
      const spot = box(bud, tree);
      const fork = limb[Math.round(limb.length * (0.25 + jitter(index * 5 + order) * 0.45))];
      const goal = { x: left ? spot.right + 3 : spot.left - 3, y: spot.y };
      const sideways = goal.x - fork.x;
      const seed = order * 31 + index * 7;
      const shoot = wobble(cubic(fork, { x: fork.x + sideways * (0.05 + jitter(seed) * 0.3), y: fork.y + (goal.y - fork.y) * (0.3 + jitter(seed + 1) * 0.4) }, { x: goal.x - sideways * (0.35 + jitter(seed + 2) * 0.4), y: goal.y + (jitter(seed + 3) - 0.5) * 30 }, goal, 32), 1.5 + jitter(seed + 4) * 3, seed);
      const kind = `shoot ${bud.parentNode.classList[1]}`;
      shapes.push(wood(shoot, 2 + jitter(seed + 5) * 1.6, 0.5, kind, ...grow(time + 450 + index * 90, 520), defs));
      shapes.push(...twiglets(shoot, Math.floor(jitter(seed + 6) * 2.4), seed, 1.4, 16, kind, animate ? time + 700 + index * 90 : null, defs));
      bud.closest(".leaf").style.setProperty("--delay", `${time + 750 + index * 90}ms`);
    });
    time += 750 + buds.length * 90;
    stops.push({ y: start.y, at: time });
    previous = start.y;
  });
  time += previous / speed;
  stops.push({ y: 0, at: time });
  const woodland = document.createElementNS(svgNS, "g");
  woodland.setAttribute("class", "woodland");
  woodland.append(...shapes);
  svg.replaceChildren(defs, woodland);
  const roots = $("roots");
  const rootSvg = $("rootlines");
  const rootWidth = roots.clientWidth;
  rootSvg.setAttribute("width", rootWidth);
  rootSvg.setAttribute("height", roots.clientHeight);
  const rootDefs = document.createElementNS(svgNS, "defs");
  const rootShapes = [];
  for (let index = 0; index < 9; index++) {
    const direction = (index / 8 - 0.5) * 2;
    const spread = direction * (90 + jitter(index * 4) * 120);
    const depth = 70 + jitter(index * 9 + 3) * 50 - Math.abs(direction) * 20;
    const base = { x: rootWidth / 2 + direction * 9, y: -14 };
    const root = cubic(base, { x: base.x + spread * 0.15, y: depth * 0.5 }, { x: base.x + spread * 0.6, y: depth * 0.75 }, { x: base.x + spread, y: depth }, 30);
    rootShapes.push(wood(root, 9 - Math.abs(direction) * 5, 0.5, "rootwood", ...grow(0, 700), rootDefs));
  }
  const rootland = document.createElementNS(svgNS, "g");
  rootland.setAttribute("class", "woodland");
  rootland.append(...rootShapes);
  rootSvg.replaceChildren(rootDefs, rootland);
  return { tip, height, stops, total: time };
}

function tipAt(stops, height, time) {
  let from = { y: height, at: 500 };
  if (time <= from.at) return height;
  for (const stop of stops) {
    if (time <= stop.at) return from.y + (stop.y - from.y) * ((time - from.at) / Math.max(1, stop.at - from.at));
    from = stop;
  }
  return 0;
}

let pieces = 0;

function collage(images, stops, total) {
  const layer = $("collage");
  const blends = ["normal", "screen", "overlay", "luminosity", "soft-light", "lighten", "hard-light"];
  for (const source of images) {
    const index = pieces++;
    const seed = jitter(index + 7);
    const other = jitter(index + 101);
    const card = el("img", "piece");
    card.alt = "";
    card.decoding = "async";
    card.referrerPolicy = "no-referrer";
    const angle = other * Math.PI * 2;
    card.style.left = `${-6 + seed * 112}%`;
    card.style.top = `${-6 + other * 112}%`;
    card.style.setProperty("--turn", `${(seed - 0.5) * 70}deg`);
    card.style.setProperty("--spin", `${(other - 0.5) * 140}deg`);
    card.style.setProperty("--size", `${0.55 + jitter(index + 211) * 1.1}`);
    card.style.setProperty("--from-x", `${Math.cos(angle) * 60}vw`);
    card.style.setProperty("--from-y", `${Math.sin(angle) * 60}vh`);
    card.style.setProperty("--wander-x", `${(jitter(index + 601) - 0.5) * 8}vw`);
    card.style.setProperty("--wander-y", `${(jitter(index + 701) - 0.5) * 8}vh`);
    card.style.setProperty("--float", `${10 + jitter(index + 307) * 12}s`);
    card.style.setProperty("--opacity", `${0.22 + jitter(index + 401) * 0.4}`);
    card.style.mixBlendMode = blends[index % blends.length];
    card.style.zIndex = String(Math.floor(seed * 10));
    const at = index < stops.length ? stops[index].at : jitter(index + 503) * Math.max(total, 4000);
    card.addEventListener("error", () => card.remove(), { once: true });
    card.addEventListener("load", () => setTimeout(() => card.classList.add("shown"), at), { once: true });
    card.src = source;
    layer.append(card);
  }
}

function climb(growth) {
  const tree = $("tree");
  scrollTo({ top: document.documentElement.scrollHeight, behavior: "instant" });
  let stopped = false;
  const stop = () => (stopped = true);
  for (const event of ["wheel", "touchstart", "keydown", "mousedown"]) {
    addEventListener(event, stop, { once: true, passive: true });
  }
  const start = performance.now();
  requestAnimationFrame(function step(now) {
    const y = tipAt(growth.stops, growth.height, now - start);
    growth.tip.setAttribute("y", `${y}`);
    growth.tip.setAttribute("height", `${growth.height + 40 - y}`);
    if (!stopped) {
      const top = tree.getBoundingClientRect().top + scrollY;
      scrollTo({ top: Math.max(0, top + y - innerHeight * 0.55), behavior: "instant" });
    }
    if (now - start < growth.total) {
      requestAnimationFrame(step);
      return;
    }
    grown = true;
    if (!stopped) scrollTo({ top: 0, behavior: "smooth" });
  });
}

const periods = [
  ["day", "Today"],
  ["yesterday", "Since yesterday"],
  ["three", "Last three days"],
  ["week", "Last seven days"],
  ["pick", "From a time…"],
];

function periodStart(kind, journeyStart) {
  const [hour, minute] = journeyStart.split(":").map(Number);
  const day = new Date();
  day.setHours(hour, minute, 0, 0);
  if (day > new Date()) day.setDate(day.getDate() - 1);
  const back = { yesterday: 1, three: 2, week: 6 }[kind] ?? 0;
  day.setDate(day.getDate() - back);
  return day;
}

function local(date) {
  const pad = value => String(value).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function renderPeriod(welcome) {
  const nav = $("period");
  const since = welcome.since ? new Date(welcome.since) : periodStart("day", welcome.journey_start);
  const label = el("span", "period-label", `Your journey since ${when(since.toISOString())}`);
  const select = el("select", "period-select");
  select.append(el("option", "", "Change the start…"));
  for (const [kind, text] of periods) {
    const option = el("option", "", text);
    option.value = kind;
    select.append(option);
  }
  const picker = el("input", "period-pick");
  picker.type = "datetime-local";
  picker.value = local(since);
  picker.hidden = true;
  const run = async start => {
    select.disabled = picker.disabled = true;
    label.textContent = `Growing your journey since ${when(start.toISOString())}…`;
    nav.classList.add("busy");
    try {
      await browser.slybrowser.refreshWelcome(local(start), true);
      location.reload();
    } catch (error) {
      select.disabled = picker.disabled = false;
      nav.classList.remove("busy");
      if (!/no journey is stored/.test(String(error.message ?? error))) {
        label.textContent = String(error.message ?? error);
        return;
      }
      label.textContent = `No journey since ${when(start.toISOString())} yet`;
      const generate = el("button", "period-select", "Generate");
      generate.addEventListener("click", async () => {
        generate.disabled = select.disabled = picker.disabled = true;
        label.textContent = `Growing your journey since ${when(start.toISOString())}…`;
        nav.classList.add("busy");
        try {
          await browser.slybrowser.refreshWelcome(local(start));
          location.reload();
        } catch (failure) {
          label.textContent = String(failure.message ?? failure);
          nav.classList.remove("busy");
          generate.disabled = select.disabled = picker.disabled = false;
        }
      });
      nav.append(generate);
    }
  };
  select.addEventListener("change", () => {
    if (select.value === "pick") {
      picker.hidden = false;
      picker.focus();
    } else if (select.value) {
      run(periodStart(select.value, welcome.journey_start));
    }
  });
  picker.addEventListener("change", () => picker.value && run(new Date(picker.value)));
  nav.replaceChildren(label, select, picker);
}

function paintSky(theme) {
  const fallback = [[96, 58, 160], [160, 64, 110], [56, 40, 110]];
  const colors = theme.length ? [...theme, ...fallback].slice(0, 3) : fallback;
  const [a, b, c] = colors.map(rgb => rgb.join(" "));
  const sky = $("sky");
  sky.style.setProperty("--c1", a);
  sky.style.setProperty("--c2", b);
  sky.style.setProperty("--c3", c);
}

function showWord(word) {
  if (!word?.word) {
    return;
  }
  const link = $("wotd");
  link.href = word.url;
  link.title = `Word of the day from ${word.source}`;
  const head = el("span", "wotd-head");
  head.append(el("span", "wotd-label", "Word of the day"), el("strong", "wotd-term", word.word), ...[word.pronunciation && el("span", "wotd-say", `\\${word.pronunciation}\\`), word.part && el("em", "wotd-part", word.part)].filter(Boolean));
  link.replaceChildren(head, ...(word.meaning ? [el("span", "wotd-meaning", word.meaning)] : []));
  link.hidden = false;
  setTimeout(() => link.classList.add("shown"), 2600);
}

function greet(welcome, delay) {
  setTimeout(() => {
    document.body.classList.add("greeted");
    $("hour").textContent = partOfDay(new Date());
    words($("greeting"), welcome.name ? `Welcome, ${welcome.name}` : "Welcome");
    type($("tagline"), welcome.tagline || "Without your input I am nothing.", 1200);
    showWord(welcome.word);
  }, delay);
}

browser.slybrowser.getWelcome().then(welcome => {
  clock = welcome.clock;
  paintSky(welcome.theme);
  if (welcome.art.length) {
    $("art").style.backgroundImage = `url("${welcome.art[Math.floor(Math.random() * welcome.art.length)]}")`;
    $("art").classList.add("shown");
  }
  const tree = $("tree");
  tree.style.gridTemplateRows = `repeat(${welcome.journey.length}, auto)`;
  tree.append(...welcome.journey.flatMap((entry, index) => node(entry, index, welcome.suggestions)));
  $("root-actions").append(...rootActions(welcome.suggestions, welcome.since));
  $("journey").hidden = welcome.journey.length === 0;
  document.body.classList.add("ready");
  renderPeriod(welcome);
  requestAnimationFrame(() =>
    requestAnimationFrame(() => {
      const growth = drawTree(welcome.journey.length > 0);
      collage(welcome.images ?? [], welcome.journey.length ? growth.stops : [], growth.total ?? 0);
      browser.slybrowser
        .getImages()
        .then(more => collage(more.filter(source => !welcome.images.includes(source)), [], growth.total ?? 0))
        .catch(() => {});
      document.body.classList.add("growing");
      if (welcome.journey.length) climb(growth);
      greet(welcome, welcome.journey.length ? growth.total + 300 : 0);
    })
  );
});

addEventListener("resize", () => requestAnimationFrame(() => grown && drawTree(false)));
document.addEventListener("pointermove", event => {
  document.body.style.setProperty("--drift-x", `${(event.clientX / innerWidth - 0.5) * -14}px`);
  document.body.style.setProperty("--drift-y", `${(event.clientY / innerHeight - 0.5) * -10}px`);
});

