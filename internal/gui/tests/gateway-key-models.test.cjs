// A gateway key's model whitelist (#882): the badge on its row, and the
// picker under it, staged until Save.
const assert = require("node:assert/strict");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { fixture } = require("./fixtures/caller-keys.cjs");

// the picker's catalog: two providers, one a single model
const catalog = () => [
  { id: "relay/m", name: "Model", provider: "relay", providerName: "Relay" },
  { id: "relay/big", name: "Big", provider: "relay", providerName: "Relay" },
  { id: "other/n", name: "Nother", provider: "other", providerName: "Other" },
];
const models = () => ({
  laptop: ["relay/m", "gone/x"],
  server: ["relay/*"],
});

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh"]) {
    test(`${engine} ${lang}: a gateway key's models are picked and set with Save`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      t.after(() => browser.close());
      const page = await (await browser.newContext({ viewport: { width: 1000, height: 420 }, reducedMotion: "reduce" })).newPage();
      page.setDefaultTimeout(6000);
      const events = [], errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.route("**/*", fixture(lang, "light", events, { lan: true, models: models(), catalog: catalog() }));
      const zh = lang === "zh";
      const w = zh
        ? { set: "限制此密钥可用的模型", change: "修改此密钥可用的模型", one: "1 个模型", two: "2 个模型", all: "全部模型", every: /Relay 的全部模型/, gone: "已不在目录中", save: "保存", cancel: "取消", unsaved: "未保存", hint: /全部模型/, makes: "取消全部勾选；保存后生效" }
        : { set: "Limit the models this key may use", change: "Change the models this key may use", one: "1 model", two: "2 models", all: "Every model", every: /Every model of Relay/, gone: "No longer in the catalog", save: "Save", cancel: "Cancel", unsaved: "unsaved", hint: /takes every model/, makes: "Unchecks them all; Save makes it" };
      await page.goto("http://magpie.test/?view=gateway");
      await page.locator("#gatewayKeys .acc[data-key]").last().waitFor();
      const row = (id) => page.locator(`#gatewayKeys .acc[data-key="${id}"]`);
      const badge = (id) => row(id).locator(".amodels.models");
      // the badge: how many models a limited key takes, always shown; a key
      // with none says Models on hover
      assert.equal(await badge("laptop").textContent(), w.two); // one in the catalog, one the catalog no longer has
      assert.equal(await badge("server").textContent(), w.one); // relay/* is one entry
      assert.equal(await badge("work").textContent(), zh ? "模型" : "Models");
      assert.equal(await badge("work").getAttribute("aria-expanded"), "false");
      // open Work's picker: staged, nothing sent
      await badge("work").click();
      const ed = row("work").locator(".key-models-ed");
      await ed.waitFor();
      assert.equal(await badge("work").getAttribute("aria-expanded"), "true");
      assert.equal(await ed.getByRole("checkbox", { name: "relay/m" }).isChecked(), false);
      assert.equal(await ed.getByRole("button", { name: w.save, exact: true }).isDisabled(), true);
      // one of a provider's every-model rows takes all of them at once
      assert.match(await ed.getByRole("checkbox", { name: w.every }).getAttribute("aria-label"), w.every);
      await ed.getByRole("checkbox", { name: w.every }).check();
      assert.equal(await ed.locator(".munsaved").textContent(), w.unsaved);
      await ed.getByRole("checkbox", { name: "other/n" }).check();
      assert.equal(events.filter((e) => e.action === "models-key").length, 0, "a check was sent before Save");
      await ed.getByRole("button", { name: w.save, exact: true }).click();
      await ed.waitFor({ state: "detached" });
      assert.deepEqual(events.filter((e) => e.action === "models-key").map((e) => e.body),
        [{ key: "work", models: ["relay/*", "other/n"] }]);
      assert.equal(await badge("work").textContent(), w.two);
      // Cancel drops what was staged
      await badge("work").click();
      await row("work").locator(".key-models-ed").waitFor();
      await row("work").locator(".key-models-ed").getByRole("checkbox", { name: "relay/m" }).check();
      await row("work").locator(".key-models-ed").getByRole("button", { name: w.cancel, exact: true }).click();
      await row("work").locator(".key-models-ed").waitFor({ state: "detached" });
      assert.equal(events.filter((e) => e.action === "models-key").length, 1, "Cancel sent the models");
      assert.equal(await badge("work").textContent(), w.two);
      // Laptop's: what it keeps is checked, what the catalog no longer has
      // is kept as it is until unchecked
      await badge("laptop").click();
      const led = row("laptop").locator(".key-models-ed");
      await led.waitFor();
      assert.equal(await led.getByRole("checkbox", { name: "relay/m" }).isChecked(), true);
      assert.equal(await led.getByRole("checkbox", { name: "relay/big" }).isChecked(), false);
      assert.equal(await led.getByRole("checkbox", { name: "gone/x" }).isChecked(), true);
      assert.match(await led.locator(".km-gone").textContent(), zh ? /已不在目录中/ : /No longer in the catalog/);
      // uncheck what it has, check another, and what is sent is the set as
      // it then stands
      await led.getByRole("checkbox", { name: "gone/x" }).uncheck();
      await led.getByRole("checkbox", { name: "relay/big" }).check();
      await led.getByRole("button", { name: w.save, exact: true }).click();
      await led.waitFor({ state: "detached" });
      assert.deepEqual(events.filter((e) => e.action === "models-key").at(-1).body, { key: "laptop", models: ["relay/m", "relay/big"] });
      // Every model + Save takes the whitelist off, as null
      await badge("server").click();
      const sed = row("server").locator(".key-models-ed");
      await sed.waitFor();
      const every = sed.getByRole("button", { name: w.all, exact: true });
      assert.equal(await every.getAttribute("title"), w.makes);
      await every.click();
      assert.equal(await sed.getByRole("checkbox", { name: w.every }).isChecked(), false);
      await sed.getByRole("button", { name: w.save, exact: true }).click();
      await sed.waitFor({ state: "detached" });
      assert.deepEqual(events.filter((e) => e.action === "models-key").at(-1).body, { key: "server", models: null });
      assert.equal(await badge("server").textContent(), zh ? "模型" : "Models");
      // the open editor fits a narrow window
      await badge("laptop").click();
      await row("laptop").locator(".key-models-ed").waitFor();
      await page.setViewportSize({ width: 560, height: 740 });
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false);
      assert.deepEqual(errors, []);
    });
  }
}
