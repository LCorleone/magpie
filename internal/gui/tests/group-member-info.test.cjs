// Run with Node's test runner and Playwright on the module path; see README.md.
// A routing group's member could not be read for what it is: the editor said
// its name, its provider and the reasoning it is sent at, and nothing of
// whether it sees images — which member a picture would reach was found out by
// sending one. Each member now carries the same chips the Gateway page's model
// list says a model in (modelInfo): its reasoning levels, whether it sees
// images and the window it holds. A member whose list says nothing of images
// is marked unknown, not "text only": magpie counts it text-only for a
// describer (gateway.blindTo), which is not its list saying it takes none. In
// English and Chinese, Chromium and WebKit.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
// three members, written as groupsState() emits them: one that sees images,
// one whose list says it takes none, and one nothing was read of. `images` is
// omitempty, so a model that doesn't take them carries no images key at all;
// only a model nothing was read of carries imagesUnknown, and it is sent only
// when true — which is what the page reads as unknown.
const models = [
  { id: "p/eye", name: "Eye", providerName: "P", icon: "generic", context: 1048576, efforts: ["low", "medium", "high"], images: true },
  { id: "p/text", name: "Text", providerName: "P", icon: "generic", context: 200000 },
  { id: "p/mystery", name: "Mystery", providerName: "P", icon: "generic", context: 128000, imagesUnknown: true },
];
const group = {
  id: "g", name: "Mixed", routing: "", ready: true,
  members: models.map((m) => m.id),
  memberInfo: models.map((m) => ({
    id: m.id, ready: true, name: m.name, provider: "p", model: m.id, icon: "generic", context: m.context,
    ...(m.images ? { images: true } : {}), ...(m.imagesUnknown ? { imagesUnknown: true } : {}),
  })),
};

const words = {
  en: { images: "Accepts images", text: "Text only", unknown: "Images: not known", levels: "low–high" },
  zh: { images: "支持图片输入", text: "仅文本", unknown: "图片：未知", levels: "低–高" },
};

function serve(lang) {
  const groups = () => ({ models, pools: [], deciders: [], found: false, groups: [group] });
  const state = { agents: [], profiles: [], settings: { lang, theme: "light" } };
  return async (r) => {
    const url = new URL(r.request().url());
    const json = (data) => r.fulfill({ json: data });
    if (url.pathname === "/boot.js") return r.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return r.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json(state);
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/gateway/trace") {
      if (url.searchParams.get("wait")) return new Promise(() => {}); // nothing more comes
      return json({ mine: true, now: new Date().toISOString(), seq: 1, totals: { requests: 0, rerouted: 0, errors: 0 }, routes: [] });
    }
    if (url.pathname === "/api/gateway/history") return json({ cut: false, days: [], routes: [] });
    if (url.pathname === "/api/groups") return json(groups());
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true, url: "http://127.0.0.1:3999" } });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await r.fulfill({ body: await fs.readFile(file), contentType });
  };
}

// each member row's chips: the badges drawn, and the lines of its tooltip
const rows = (page) => page.locator(".rt-gedit .fbrow").evaluateAll((rs) => rs.map((r) => {
  const info = r.querySelector(".minfo");
  return {
    name: (r.querySelector(".n > span") || {}).textContent || "",
    badges: info ? [...info.querySelectorAll(".badge")].map((b) => b.textContent.trim() || "img") : [],
    title: info ? info.title : "",
    img: !!(info && info.querySelector(".mi-img")),
  };
}));

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh"]) {
    const w = words[lang];
    test(`${engine} ${lang}: a group's members say what each can do`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      t.after(async () => {
        if (process.env.ARTIFACT_DIR) {
          await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
          await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-member-info.png`) });
        }
        await browser.close();
      });
      const page = await (await browser.newContext({ viewport: { width: 1100, height: 800 }, reducedMotion: "reduce" })).newPage();
      page.setDefaultTimeout(5000);
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.route("**/*", serve(lang));
      await page.goto("http://magpie.test/?view=routing");
      await page.locator(".rt-group[data-id=g]").waitFor();
      await page.locator(".rt-group[data-id=g]").click();
      await page.locator(".rt-gedit .fbrow").first().waitFor();

      const seen = await rows(page);
      assert.equal(seen.length, 3, "every member has its row");
      assert.deepEqual(seen.map((r) => r.name), ["Eye", "Text", "Mystery"], "the rows are the members, in order");

      // what it sees: the badge on the one that sees, and no badge on the others
      assert(seen[0].img, "the member that sees images carries the image chip");
      assert(!seen[1].img && !seen[2].img, "a member that does not see carries none");

      // and the same said in words, in the tooltip
      assert(seen[0].title.includes(w.images), `the seeing member says so: ${seen[0].title}`);
      assert(seen[1].title.includes(w.text), `the text-only member says so: ${seen[1].title}`);
      assert(seen[2].title.includes(w.unknown), `a member nothing was read of says unknown, not text only: ${seen[2].title}`);
      assert(!seen[2].title.includes(w.text), "and is not called text only");

      // the rest of the chips: its levels and the window it holds
      assert(seen[0].badges.includes(w.levels), `its reasoning levels: ${JSON.stringify(seen[0].badges)}`);
      assert(seen[0].badges.includes("1.0M"), `the window it holds: ${JSON.stringify(seen[0].badges)}`);
      assert.deepEqual(errors, [], "no page error");

      // the row is still the row it was: switching it off and removing it
      const off = page.locator(".rt-gedit .fbrow").first().locator(".rt-mon");
      await off.click();
      assert.equal(await off.getAttribute("aria-checked"), "false", "the member is switched off");
      await off.click();
      assert.equal(await off.getAttribute("aria-checked"), "true", "and on again");
      // the last row is wheeled up into view as a reader would: a scroll the
      // page wasn't asked for by the reader (Playwright's own) is put back
      // after the clicks above (scrollOnPurpose), which leaves the row under
      // the footer
      await page.mouse.move(550, 400);
      await page.mouse.wheel(0, 400);
      await page.locator(".rt-gedit .fbrow").last().locator("button.text", { hasText: lang === "zh" ? "移除" : "Remove" }).click();
      assert.equal((await rows(page)).length, 2, "the last member is removed");
    });
  }
}
