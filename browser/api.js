this.slybrowser = class extends ExtensionAPI {
  onStartup() {
    Services.io
      .getProtocolHandler("resource")
      .QueryInterface(Ci.nsIResProtocolHandler)
      .setSubstitution("slybrowser", this.extension.rootURI);
    ChromeUtils.importESModule("resource://slybrowser/state.sys.mjs").State.pages = this.extension.baseURI.spec;
    ChromeUtils.importESModule("resource://slybrowser/server.sys.mjs").start();
  }

  getAPI(context) {
    const state = () => ChromeUtils.importESModule("resource://slybrowser/state.sys.mjs");
    return {
      slybrowser: {
        getWelcome: () => state().State.getWelcome(),
        getImages: () => state().State.getMoreImages(),
        applySuggestion: async (source, kind, index, page) => {
          const result = await state().runSlyb(["suggestion", "apply", source, kind, String(index), ...(page ? ["--page", page] : [])]);
          return { summary: result?.summary ?? "" };
        },
        askAssistant: async (kind, text) => {
          const args = { group: ["group", "create", "--ask", text], rule: ["rule", "create", "--ask", text] }[kind];
          const result = await state().runSlyb(args);
          return { summary: result?.summary ?? result?.name ?? "" };
        },
        postAction: async args => ({ result: JSON.stringify(await state().runSlyb(["action", ...args])) }),
        notePage: async (page, tabId) => {
          const tab = context.extension.tabManager.get(tabId, null)?.nativeTab;
          state().State.notePage(page, tab?.getAttribute("zen-workspace-id") ?? "");
        },
        refreshWelcome: async (since, cached) => {
          const result = await state().runSlyb(["welcome", "refresh", ...(since ? ["--since", since] : []), ...(cached ? ["--cached"] : [])]);
          return { summary: result?.summary ?? "" };
        },
      },
    };
  }
};
