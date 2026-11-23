browser.runtime.onMessage.addListener((message, sender) => {
  if (message?.page && sender.tab) {
    return browser.slybrowser.notePage(message.page, sender.tab.id);
  }
});
