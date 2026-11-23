const read = () => {
  const meta = name => document.querySelector(`meta[name="${name}" i], meta[property="${name}" i]`)?.getAttribute("content")?.trim() ?? "";
  const text = value => (value ?? "").replace(/\s+/g, " ").trim().slice(0, 300);
  return {
    url: location.href,
    title: text(document.title),
    description: text(meta("description") || meta("og:description") || meta("twitter:description")),
    site: text(meta("og:site_name") || meta("application-name") || meta("apple-mobile-web-app-title")),
    type: text(meta("og:type")),
    image: meta("og:image"),
    canonical: document.querySelector('link[rel="canonical"]')?.href ?? "",
    keywords: text(meta("keywords")),
    language: document.documentElement.lang ?? "",
    heading: text(document.querySelector("h1")?.textContent),
  };
};

let last = "";
let timer = 0;
const send = () => {
  const page = read();
  const key = `${page.url}|${page.title}`;
  if (key !== last) {
    last = key;
    browser.runtime.sendMessage({ page }).catch(() => {});
  }
};
const later = () => {
  clearTimeout(timer);
  timer = setTimeout(send, 1500);
};

send();
new MutationObserver(later).observe(document.querySelector("head") ?? document.documentElement, { subtree: true, childList: true, characterData: true });
addEventListener("popstate", later);
