const $ = id => document.getElementById(id);
const svgNS = "http://www.w3.org/2000/svg";
const params = new URLSearchParams(location.search);
const scope = { kind: params.get("kind") ?? "", id: params.get("id") ?? "", title: params.get("title") ?? "" };

const triggers = {
  "tab.opened": ["A tab opens", "Starts when a tab is opened, with its id, url, title, workspace and group."],
  "tab.closed": ["A tab closes", "Starts when a tab is closed."],
  "tab.selected": ["A tab is selected", "Starts when a tab gets focus."],
  "page.loaded": ["A page has loaded", "Starts when a page has finished loading in a tab."],
  "page.outside": ["A page opens from outside", "Starts when another app opens a page in the browser."],
  interval: ["On an interval", "Starts every so many minutes, set as every on the trigger."],
  "organize.done": ["After organize", "Starts when organize has sorted a workspace, with the pages it could not place."],
};

const glyphs = {
  provider: "M4 6c0-1.7 3.6-3 8-3s8 1.3 8 3-3.6 3-8 3-8-1.3-8-3zM4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3",
  trigger: "M13 3L5 14h6l-1 7 8-11h-6z",
  receiver: "M12 16V4M7 9l5-5 5 5M5 20h14",
  formatter: "M12 3a9 9 0 100 18c1.5 0 2-1 2-2s-.5-2 1-2h2a4 4 0 004-4c0-5-4-10-9-10z",
  transformer: "M4 8h16M16 4l4 4-4 4M20 16H4M8 12l-4 4 4 4",
  filter: "M4 5h16l-6 8v6l-4-2v-4z",
  static: "M8 7l-5 5 5 5M16 7l5 5-5 5",
  condition: "M12 4v6M12 10H6v4M12 10h6v4M4 14h4v5H4zM16 14h4v5h-4z",
  conclusion: "M12 3a9 9 0 100 18 9 9 0 000-18zM9 9h6v6H9z",
  fetch: "M12 3a9 9 0 100 18 9 9 0 000-18zM3 12h18M12 3c3 3 3 15 0 18M12 3c-3 3-3 15 0 18",
  action: "M4 12l16-8-6 16-3-7z",
  more: "M12 6v.01M12 12v.01M12 18v.01",
  chevron: "M6 9l6 6 6-6",
};

const state = { actions: [], catalogue: [], action: null, runs: [], test: null, revealed: 0, selected: null, pan: { x: 40, y: 60 }, view: "result", search: "", closed: new Set() };

function icon(name) {
  const svg = document.createElementNS(svgNS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  const path = document.createElementNS(svgNS, "path");
  path.setAttribute("d", glyphs[name] ?? glyphs.transformer);
  svg.append(path);
  return svg;
}

function make(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

async function call(...args) {
  const reply = await browser.slybrowser.postAction(args.map(String));
  return JSON.parse(reply.result);
}

function definition(placed) {
  if (!placed.node) return { kind: placed.kind, code: placed.code ?? "", description: "", input_schema: {} };
  return state.catalogue.find(entry => entry.key === placed.node) ?? { kind: "transformer", code: "", description: "", input_schema: {} };
}

function flowKind(placed) {
  return placed.node === "trigger" ? "trigger" : definition(placed).kind;
}

function triggerLabel(name) {
  return triggers[name]?.[0] ?? name;
}

function openDialog({ title, hint = "", fields = [], submit, run }) {
  const dialog = $("dialog");
  $("dialog-title").textContent = title;
  $("dialog-hint").textContent = hint;
  $("dialog-hint").hidden = !hint;
  $("dialog-submit").textContent = submit;
  $("dialog-error").hidden = true;
  const inputs = new Map();
  $("dialog-fields").replaceChildren(
    ...fields.map(field => {
      const label = make("label", "field");
      label.append(make("span", "field-label", field.label));
      let input;
      if (field.type === "select") {
        input = make("select");
        for (const [value, text] of field.options) {
          const option = make("option", "", text);
          option.value = value;
          input.append(option);
        }
      } else if (field.type === "code" || field.type === "json") {
        input = make("textarea");
        input.spellcheck = false;
      } else {
        input = make("input");
        input.type = field.type === "number" ? "number" : field.type === "bool" ? "checkbox" : "text";
        if (field.list) {
          const list = make("datalist");
          list.id = `list-${field.key}`;
          list.append(...field.list.map(value => Object.assign(make("option"), { value })));
          input.setAttribute("list", list.id);
          label.append(list);
        }
      }
      if (field.type === "bool") input.checked = !!field.value;
      else input.value = field.value ?? "";
      input.required = !!field.required;
      label.append(input);
      if (field.hint) label.append(make("span", "field-hint", field.hint));
      inputs.set(field.key, [input, field]);
      return label;
    })
  );
  return new Promise(resolve => {
    const form = $("dialog-form");
    const finish = value => {
      form.onsubmit = null;
      dialog.onclose = null;
      if (dialog.open) dialog.close();
      resolve(value);
    };
    form.onsubmit = async event => {
      event.preventDefault();
      if (event.submitter?.value !== "ok") return finish(null);
      const values = {};
      for (const [key, [input, field]] of inputs) {
        values[key] = field.type === "bool" ? input.checked : field.type === "number" ? (input.value === "" ? null : Number(input.value)) : input.value;
      }
      try {
        $("dialog-submit").disabled = true;
        finish((await run(values)) ?? true);
      } catch (error) {
        $("dialog-error").textContent = String(error.message ?? error);
        $("dialog-error").hidden = false;
      } finally {
        $("dialog-submit").disabled = false;
      }
    };
    dialog.onclose = () => finish(null);
    dialog.showModal();
  });
}

function openMenu(anchor, items) {
  const menu = $("menu");
  menu.replaceChildren(
    ...items.map(([label, run, danger]) => {
      const item = make("button", "menu-item", label);
      item.dataset.danger = String(!!danger);
      item.addEventListener("click", () => {
        menu.hidden = true;
        run();
      });
      return item;
    })
  );
  const box = anchor.getBoundingClientRect();
  menu.hidden = false;
  menu.style.insetBlockStart = `${box.bottom + 4}px`;
  menu.style.insetInlineStart = `${Math.min(box.left, innerWidth - menu.offsetWidth - 8)}px`;
}

async function loadActions(pick) {
  const args = scope.kind ? [`--${scope.kind}`, scope.id] : [];
  state.actions = await call("get", ...args);
  const wanted = pick ?? state.action?.id ?? params.get("action");
  const chosen = state.actions.find(entry => entry.id === wanted) ?? state.actions[0];
  await loadAction(chosen?.id ?? null);
}

async function loadAction(id) {
  if (!id) {
    state.action = null;
    state.runs = [];
  } else {
    const found = await call("get", id, "--runs");
    if (state.action?.id !== found.action.id) {
      state.test = null;
      state.selected = null;
    }
    state.action = found.action;
    state.action.nodes ??= [];
    state.action.links ??= [];
    state.runs = found.runs ?? [];
  }
  render();
}

async function change(...args) {
  try {
    await call(...args);
  } catch (error) {
    $("problems").textContent = String(error.message ?? error);
    $("problems").hidden = false;
    return;
  }
  await loadActions();
}

function render() {
  renderBar();
  renderPalette();
  renderGraph();
  renderSide();
}

function renderBar() {
  $("scope").textContent = scope.title || "The whole browser";
  document.title = `Actions · ${scope.title || "browser"}`;
  $("tabs").replaceChildren(
    ...state.actions.map(entry => {
      const tab = make("button", "tab");
      tab.dataset.status = entry.status;
      tab.dataset.problems = String(!!entry.problems?.length);
      tab.setAttribute("aria-selected", String(entry.id === state.action?.id));
      tab.title = `${entry.name}\n${entry.triggers.map(triggerLabel).join(", ") || "No trigger"}\nDouble click to rename`;
      tab.append(make("span", "tab-dot"), make("span", "", entry.name));
      tab.addEventListener("click", () => loadAction(entry.id));
      tab.addEventListener("dblclick", () =>
        openDialog({ title: "Rename the action", fields: [{ key: "name", label: "Name", value: entry.name, required: true }], submit: "Rename", run: values => call("update", entry.id, "--name", values.name) }).then(done => done && loadActions())
      );
      return tab;
    })
  );
  const action = state.action;
  for (const id of ["status", "run", "delete-action"]) $(id).hidden = !action;
  if (action) {
    $("status").textContent = action.status === "active" ? "Active" : "Paused";
    $("status").title = action.status === "active" ? "Pause the action" : "Set the action active";
  }
  $("problems").hidden = !action?.problems?.length;
  $("problems").textContent = action?.problems?.join(" · ") ?? "";
}

function paletteItems() {
  const items = Object.entries(triggers).map(([name, [title, description]]) => ({ id: `trigger:${name}`, title, description, kind: "trigger", group: "Triggers", args: ["--node", "trigger", "--name", title, "--input", JSON.stringify({ trigger: name })] }));
  for (const entry of state.catalogue.filter(entry => entry.key !== "trigger")) {
    items.push({ id: `node:${entry.key}`, title: entry.name, description: entry.description, kind: entry.kind, group: "Nodes", args: ["--node", entry.key] });
  }
  items.push(
    { id: "own:condition", title: "Condition", description: "Code of your own that returns true or false and branches the action.", kind: "condition", group: "Code", args: ["--kind", "condition", "--name", "Condition", "--code", "return true"] },
    { id: "own:transformer", title: "Code", description: "Code of your own that reads data and returns what the next node receives.", kind: "transformer", group: "Code", args: ["--kind", "transformer", "--name", "Code", "--code", "return data"] },
    { id: "own:filter", title: "Filter", description: "Code of your own that returns the list it keeps.", kind: "filter", group: "Code", args: ["--kind", "filter", "--name", "Filter", "--code", "return data"] }
  );
  return items;
}

function renderPalette() {
  const words = state.search.toLowerCase().split(/\s+/).filter(Boolean);
  const groups = new Map();
  for (const item of paletteItems()) {
    if (words.every(word => `${item.title} ${item.description}`.toLowerCase().includes(word))) {
      groups.set(item.group, [...(groups.get(item.group) ?? []), item]);
    }
  }
  $("palette-groups").replaceChildren(
    ...[...groups].map(([label, items]) => {
      const group = make("div", "group");
      group.dataset.open = String(!state.closed.has(label));
      const head = make("button", "group-label");
      head.append(icon("chevron"), make("span", "", label));
      head.addEventListener("click", () => {
        state.closed.has(label) ? state.closed.delete(label) : state.closed.add(label);
        renderPalette();
      });
      const list = make("div", "group-items");
      list.append(
        ...items.map(item => {
          const row = make("button", "item");
          row.style.setProperty("--tone", `var(--${{ trigger: "yellow", condition: "orange", transformer: "teal", filter: "amber", action: "green", provider: "blue" }[item.kind] ?? "muted"})`);
          row.draggable = true;
          const text = make("span", "item-text");
          text.append(make("span", "", item.title), make("span", "item-detail", item.description));
          row.append(icon(item.kind), text);
          row.addEventListener("click", () => addNode(item, null));
          row.addEventListener("dragstart", event => event.dataTransfer.setData("text/sly-node", item.id));
          return row;
        })
      );
      group.append(head, list);
      return group;
    })
  );
}

async function addNode(item, at) {
  if (!state.action) {
    const made = await newAction();
    if (!made) return;
  }
  const taken = state.action.nodes.map(placed => placed.position ?? { x: 0, y: 0 });
  const spot = at ?? { x: taken.length ? Math.max(...taken.map(point => point.x)) + 280 : 0, y: taken.length ? taken[taken.length - 1].y : 0 };
  await change("node", "create", state.action.id, ...item.args, "--x", Math.round(spot.x), "--y", Math.round(spot.y));
}

function newAction() {
  return openDialog({
    title: "New action",
    hint: `An action for ${scope.title || "the whole browser"}. It starts on an event and runs its nodes in order.`,
    fields: [
      { key: "name", label: "Name", required: true },
      { key: "trigger", label: "Starts when", type: "select", options: [["", "Pick a trigger later"], ...Object.entries(triggers).map(([name, [title]]) => [name, title])] },
    ],
    submit: "Create action",
    run: async values => {
      const made = await call("create", values.name, ...(scope.kind ? [`--${scope.kind}`, scope.id] : []), ...(values.trigger ? ["--trigger", values.trigger] : []));
      await loadActions(made.id);
    },
  });
}

function testState(placed) {
  if (!state.test) return "";
  if (state.test === "running") return "running";
  const index = state.test.nodes.findIndex(entry => entry.id === placed.id);
  if (index === -1) return state.revealed >= state.test.nodes.length ? "skipped" : "running";
  return index < state.revealed ? state.test.nodes[index].status : "running";
}

function renderGraph() {
  const action = state.action;
  $("empty").hidden = !!action?.nodes.length;
  $("empty").textContent = action ? "Pick a trigger from the catalogue to start the action." : "No action here yet. Make one with New action.";
  $("world").style.transform = `translate(${state.pan.x}px, ${state.pan.y}px)`;
  $("nodes").replaceChildren(
    ...(action?.nodes ?? []).map((placed, index) => {
      const kind = flowKind(placed);
      const entry = definition(placed);
      const node = make("div", "node");
      node.dataset.id = placed.id;
      node.dataset.kind = kind;
      node.dataset.selected = String(state.selected?.kind === "node" && state.selected.id === placed.id);
      const tested = testState(placed);
      if (tested) node.dataset.state = tested;
      const position = placed.position ?? { x: index * 280, y: 0 };
      node.style.insetInlineStart = `${position.x}px`;
      node.style.insetBlockStart = `${position.y}px`;
      const head = make("div", "node-head");
      const more = make("button", "icon-btn node-more");
      more.title = "More";
      more.append(icon("more"));
      more.addEventListener("pointerdown", event => event.stopPropagation());
      more.addEventListener("click", event => {
        event.stopPropagation();
        openMenu(more, [
          ["Configure", () => configure(placed)],
          ["Delete", () => change("node", "delete", action.id, placed.id), true],
        ]);
      });
      head.append(icon(kind), make("span", "node-name", kind === "trigger" ? triggerLabel(placed.input?.trigger) : placed.name), more);
      node.append(head);
      const description = kind === "trigger" ? triggers[placed.input?.trigger]?.[1] : entry.description;
      if (description) node.append(make("span", "node-description", description));
      const details = Object.entries(placed.input ?? {}).filter(([key, value]) => key !== "trigger" && value !== "" && value !== null && !(key === "every" && !value));
      if (details.length) {
        const list = make("dl", "node-details");
        for (const [key, value] of details) {
          const row = make("div", "node-detail");
          row.title = `${key}: ${value}`;
          row.append(make("dt", "", key), make("dd", "", typeof value === "object" ? JSON.stringify(value) : String(value)));
          list.append(row);
        }
        node.append(list);
      }
      if (kind !== "trigger" && kind !== "provider" && kind !== "static") node.append(make("span", "handle handle-in"));
      const out = type => {
        const handle = make("span", "handle handle-out");
        handle.dataset.type = type;
        handle.addEventListener("pointerdown", event => startLink(event, placed, handle));
        return handle;
      };
      if (kind === "condition") {
        const anchors = make("div", "node-anchors");
        for (const type of ["true", "false"]) {
          const anchor = make("span", "node-anchor", type);
          anchor.append(out(type));
          anchors.append(anchor);
        }
        node.append(anchors);
      } else if (kind !== "receiver" && kind !== "conclusion") {
        node.append(out("object"));
      }
      const failed = tested === "failed" ? state.test.nodes.find(run => run.id === placed.id)?.error : "";
      if (failed) {
        const error = make("div", "node-error", failed);
        error.title = failed;
        node.append(error);
      }
      node.addEventListener("pointerdown", event => startDrag(event, placed, node));
      node.addEventListener("dblclick", () => configure(placed));
      return node;
    })
  );
  renderEdges();
}

function anchorPoint(id, type, side) {
  const node = $("nodes").querySelector(`.node[data-id="${id}"]`);
  if (!node) return null;
  if (side === "in") return { x: node.offsetLeft, y: node.offsetTop + node.offsetHeight / 2 };
  const handle = node.querySelector(`.handle-out[data-type="${type}"]`) ?? node.querySelector(".handle-out");
  const anchor = handle?.closest(".node-anchor");
  return { x: node.offsetLeft + node.offsetWidth, y: node.offsetTop + (anchor ? anchor.offsetTop + anchor.offsetHeight / 2 : node.offsetHeight / 2) };
}

function curve(from, to) {
  const bend = Math.max(40, Math.abs(to.x - from.x) / 2);
  return `M${from.x},${from.y} C${from.x + bend},${from.y} ${to.x - bend},${to.y} ${to.x},${to.y}`;
}

function renderEdges(draft) {
  const paths = [];
  const ran = state.test && state.test !== "running" ? new Set(state.test.nodes.slice(0, state.revealed).map(entry => entry.id)) : null;
  for (const link of state.action?.links ?? []) {
    const from = anchorPoint(link.from, link.type, "out");
    const to = anchorPoint(link.to, link.type, "in");
    if (!from || !to) continue;
    const path = document.createElementNS(svgNS, "path");
    path.setAttribute("class", "edge");
    path.setAttribute("d", curve(from, to));
    path.dataset.type = link.type;
    path.dataset.selected = String(state.selected?.kind === "link" && state.selected.id === link.id);
    if (ran) path.dataset.state = ran.has(link.from) && ran.has(link.to) ? "passed" : "idle";
    const hit = document.createElementNS(svgNS, "path");
    hit.setAttribute("class", "edge-hit");
    hit.setAttribute("d", curve(from, to));
    hit.addEventListener("pointerdown", event => {
      event.stopPropagation();
      state.selected = { kind: "link", id: link.id };
      renderGraph();
    });
    paths.push(path, hit);
  }
  if (draft) {
    const path = document.createElementNS(svgNS, "path");
    path.setAttribute("class", "edge-draft");
    path.setAttribute("d", curve(draft.from, draft.to));
    paths.push(path);
  }
  $("edges").replaceChildren(...paths);
}

function worldPoint(event) {
  const box = $("canvas").getBoundingClientRect();
  return { x: event.clientX - box.left - state.pan.x, y: event.clientY - box.top - state.pan.y };
}

function track(event, move, end) {
  const target = event.currentTarget;
  target.setPointerCapture(event.pointerId);
  const onMove = moved => move(moved);
  const onEnd = ended => {
    target.removeEventListener("pointermove", onMove);
    target.removeEventListener("pointerup", onEnd);
    target.removeEventListener("pointercancel", onEnd);
    end(ended);
  };
  target.addEventListener("pointermove", onMove);
  target.addEventListener("pointerup", onEnd);
  target.addEventListener("pointercancel", onEnd);
}

function startDrag(event, placed, node) {
  if (event.button !== 0 || event.target.closest(".handle-out")) return;
  event.stopPropagation();
  const start = worldPoint(event);
  const origin = { x: node.offsetLeft, y: node.offsetTop };
  let moved = false;
  track(
    event,
    at => {
      const point = worldPoint(at);
      moved = moved || Math.hypot(point.x - start.x, point.y - start.y) > 3;
      if (!moved) return;
      node.style.insetInlineStart = `${origin.x + point.x - start.x}px`;
      node.style.insetBlockStart = `${origin.y + point.y - start.y}px`;
      renderEdges();
    },
    () => {
      if (!moved) {
        state.selected = { kind: "node", id: placed.id };
        return renderGraph();
      }
      placed.position = { x: node.offsetLeft, y: node.offsetTop };
      call("node", "update", state.action.id, placed.id, "--x", node.offsetLeft, "--y", node.offsetTop).catch(error => console.error(error));
    }
  );
}

function startLink(event, placed, handle) {
  if (event.button !== 0) return;
  event.stopPropagation();
  event.preventDefault();
  const from = anchorPoint(placed.id, handle.dataset.type, "out");
  track(
    event,
    at => renderEdges({ from, to: worldPoint(at) }),
    ended => {
      renderEdges();
      const target = document.elementFromPoint(ended.clientX, ended.clientY)?.closest(".node");
      if (target && target.dataset.id !== placed.id) {
        change("link", "create", state.action.id, placed.id, target.dataset.id, "--type", handle.dataset.type);
      }
    }
  );
}

function configure(placed) {
  const entry = definition(placed);
  const kind = flowKind(placed);
  const schema = entry.input_schema ?? {};
  const keys = [...new Set([...Object.keys(schema), ...Object.keys(placed.input ?? {})])];
  const fields = [{ key: "name", label: "Name", value: placed.name, required: true }];
  for (const key of keys) {
    const declared = schema[key] ?? {};
    const value = placed.input?.[key];
    fields.push({
      key: `input.${key}`,
      label: key,
      hint: declared.description ?? "",
      type: declared.type === "number" ? "number" : typeof value === "object" && value !== null ? "json" : "text",
      value: typeof value === "object" && value !== null ? JSON.stringify(value, null, 2) : value ?? "",
      required: !!declared.required,
      list: key === "trigger" ? Object.keys(triggers) : null,
    });
  }
  if (kind !== "trigger" && kind !== "provider") {
    fields.push({ key: "code", label: "Code", type: "code", value: entry.code, hint: placed.node ? "This is the catalogue's code. Change it and the node becomes a node of its own." : "Tengo. Reads input and data, returns what the next node receives. Helpers: tabs, page, content, github, place, group, order, live, emit." });
  }
  return openDialog({
    title: `Configure ${placed.name}`,
    hint: entry.description,
    fields,
    submit: "Save",
    run: async values => {
      const input = {};
      for (const key of keys) {
        const raw = values[`input.${key}`];
        if (raw === "" || raw === null) continue;
        const wasObject = typeof placed.input?.[key] === "object" && placed.input[key] !== null;
        input[key] = wasObject ? JSON.parse(raw) : raw;
      }
      const args = ["node", "update", state.action.id, placed.id, "--name", values.name, "--input", JSON.stringify(input)];
      if (values.code !== undefined && values.code.trim() !== entry.code.trim()) args.push("--code", values.code);
      await call(...args);
      await loadActions();
    },
  });
}

const samples = {
  "organize.done": { workspace: "", unplaced: [{ url: "https://example.org/a", title: "Example" }] },
  interval: {},
};

function runAction() {
  const action = state.action;
  const started = [...new Set(action.nodes.filter(placed => placed.node === "trigger").map(placed => placed.input?.trigger).filter(Boolean))];
  if (!started.length) {
    $("problems").textContent = "The action has no trigger to run from.";
    $("problems").hidden = false;
    return;
  }
  const key = `sly-action-data:${action.id}`;
  const stored = localStorage.getItem(key);
  return openDialog({
    title: `Run ${action.name}`,
    hint: "Runs the nodes with this event. A test records what each effect would do and changes nothing.",
    fields: [
      { key: "trigger", label: "Event", type: "select", options: started.map(name => [name, triggerLabel(name)]) },
      { key: "data", label: "Event data", type: "json", value: stored ?? JSON.stringify(samples[started[0]] ?? { url: "https://example.org/", title: "Example", workspace: scope.kind === "workspace" ? scope.id : "" }, null, 2) },
      { key: "real", label: "Carry out the effects", type: "bool", value: false, hint: "Off is a test. On places, groups and orders for real and records the run." },
    ],
    submit: "Run",
    run: async values => {
      JSON.parse(values.data || "{}");
      localStorage.setItem(key, values.data);
      state.test = "running";
      state.revealed = 0;
      state.view = "result";
      $("frame").dataset.side = "open";
      $("side-toggle").setAttribute("aria-pressed", "true");
      render();
      const result = await call("run", values.trigger, "--action", action.id, "--data", values.data || "{}", ...(values.real ? [] : ["--dry"]));
      state.test = result.runs[0] ?? null;
      if (values.real) await loadActions();
      render();
      const timer = setInterval(() => {
        state.revealed += 1;
        render();
        if (!state.test || state.test === "running" || state.revealed >= state.test.nodes.length) clearInterval(timer);
      }, 300);
    },
  });
}

function runCard(run, open) {
  const card = make("div", "run");
  const head = make("div", "run-head");
  const status = make("span", "run-status", run.dry ? `Test ${run.status}` : run.status);
  status.dataset.status = run.status;
  head.append(status, make("span", "", triggerLabel(run.trigger)), make("span", "run-when", new Date(run.started_at).toLocaleString(undefined, { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", second: "2-digit" })));
  const counts = make("div", "run-counts");
  counts.append(make("span", "", `${run.total} nodes`), make("span", "", `${run.succeeded} ok`), make("span", "", `${run.failed} failed`), make("span", "", `${run.effects?.length ?? 0} effects`));
  card.append(head, counts);
  if (run.error) card.append(make("div", "run-error", run.error));
  if (!open) return card;
  const names = new Map((state.action?.nodes ?? []).map(placed => [placed.id, flowKind(placed) === "trigger" ? triggerLabel(placed.input?.trigger) : placed.name]));
  for (const entry of run.nodes ?? []) {
    const step = make("div", "step");
    step.dataset.status = entry.status;
    const top = make("div", "step-head");
    top.append(make("span", "step-dot"), make("strong", "", names.get(entry.id) ?? entry.id), make("span", "step-time", `${entry.duration_ms} ms`));
    step.append(top);
    if (entry.error) step.append(make("div", "run-error", entry.error));
    if (entry.result !== undefined) step.append(make("pre", "json", JSON.stringify(entry.result, null, 2)));
    card.append(step);
  }
  if (run.effects?.length) {
    const step = make("div", "step");
    step.append(make("strong", "", "Effects"), make("pre", "json", JSON.stringify(run.effects, null, 2)));
    card.append(step);
  }
  return card;
}

function renderSide() {
  for (const tab of document.querySelectorAll(".side-tab")) {
    tab.setAttribute("aria-selected", String(tab.dataset.view === state.view));
  }
  const body = $("side-body");
  if (state.view === "result") {
    if (state.test === "running") return body.replaceChildren(make("p", "side-empty", "Running…"));
    if (!state.test) return body.replaceChildren(make("p", "side-empty", "Run the action to see what each node returns."));
    return body.replaceChildren(runCard(state.test, true));
  }
  if (!state.runs.length) return body.replaceChildren(make("p", "side-empty", "No runs yet. A run is recorded each time an event starts the action."));
  body.replaceChildren(
    ...state.runs.map(run => {
      const card = runCard(run, false);
      card.addEventListener("click", () => {
        state.test = run;
        state.revealed = run.nodes.length;
        state.view = "result";
        render();
      });
      return card;
    })
  );
}

$("palette-toggle").addEventListener("click", () => {
  const open = $("frame").dataset.palette !== "open";
  $("frame").dataset.palette = open ? "open" : "closed";
  $("palette-toggle").setAttribute("aria-pressed", String(open));
});
$("side-toggle").addEventListener("click", () => {
  const open = $("frame").dataset.side !== "open";
  $("frame").dataset.side = open ? "open" : "closed";
  $("side-toggle").setAttribute("aria-pressed", String(open));
});
$("search").addEventListener("input", event => {
  state.search = event.target.value;
  renderPalette();
});
$("new-action").addEventListener("click", newAction);
$("run").addEventListener("click", runAction);
$("status").addEventListener("click", () => change("update", state.action.id, "--status", state.action.status === "active" ? "paused" : "active"));
$("delete-action").addEventListener("click", () =>
  openDialog({ title: `Delete ${state.action.name}?`, hint: "Its nodes, links and runs go with it. This cannot be undone.", submit: "Delete", run: () => call("delete", state.action.id) }).then(done => {
    if (done) {
      state.action = null;
      loadActions();
    }
  })
);
for (const tab of document.querySelectorAll(".side-tab")) {
  tab.addEventListener("click", () => {
    state.view = tab.dataset.view;
    renderSide();
  });
}
$("canvas").addEventListener("pointerdown", event => {
  if (event.button !== 0 || event.target.closest(".node")) return;
  $("menu").hidden = true;
  const start = { x: event.clientX - state.pan.x, y: event.clientY - state.pan.y };
  $("canvas").dataset.panning = "true";
  let moved = false;
  track(
    event,
    at => {
      moved = true;
      state.pan = { x: at.clientX - start.x, y: at.clientY - start.y };
      $("world").style.transform = `translate(${state.pan.x}px, ${state.pan.y}px)`;
    },
    () => {
      $("canvas").dataset.panning = "false";
      if (!moved && state.selected) {
        state.selected = null;
        renderGraph();
      }
    }
  );
});
$("canvas").addEventListener("wheel", event => {
  event.preventDefault();
  state.pan = { x: state.pan.x - event.deltaX, y: state.pan.y - event.deltaY };
  $("world").style.transform = `translate(${state.pan.x}px, ${state.pan.y}px)`;
}, { passive: false });
$("canvas").addEventListener("dragover", event => event.preventDefault());
$("canvas").addEventListener("drop", event => {
  event.preventDefault();
  const item = paletteItems().find(entry => entry.id === event.dataTransfer.getData("text/sly-node"));
  if (item) addNode(item, worldPoint(event));
});
document.addEventListener("pointerdown", event => {
  if (!event.target.closest("#menu") && !event.target.closest(".node-more")) $("menu").hidden = true;
});
document.addEventListener("keydown", event => {
  if ((event.key !== "Delete" && event.key !== "Backspace") || !state.selected || $("dialog").open || event.target.closest("input, textarea")) return;
  const picked = state.selected;
  state.selected = null;
  change(picked.kind, "delete", state.action.id, picked.id);
});
window.addEventListener("resize", () => renderEdges());

call("node", "get")
  .then(catalogue => {
    state.catalogue = catalogue;
    return loadActions();
  })
  .catch(error => {
    $("problems").textContent = String(error.message ?? error);
    $("problems").hidden = false;
  });
