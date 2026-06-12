import { chromium } from "playwright";
const b = await chromium.launch();
const p = await b.newPage({ viewport: { width: 1280, height: 900 } });
await p.goto("http://localhost:3000/login", { waitUntil: "networkidle" });
await p.evaluate(() => { localStorage.setItem("loadify_token","fake"); localStorage.setItem("loadify_user", JSON.stringify({id:"u1",email:"a@b.c",name:"王少凯",role:"admin"})); });
await p.goto("http://localhost:3000/tests", { waitUntil: "networkidle" });
await p.waitForTimeout(400); await p.click("text=新建用例"); await p.waitForTimeout(400);
await p.screenshot({ path: "/tmp/ui8-form.png", fullPage: true });
// narrow viewport
const p2 = await b.newPage({ viewport: { width: 420, height: 800 } });
await p2.goto("http://localhost:3000/login", { waitUntil: "networkidle" });
await p2.evaluate(() => { localStorage.setItem("loadify_token","fake"); localStorage.setItem("loadify_user", JSON.stringify({id:"u1",email:"a@b.c",name:"王",role:"admin"})); });
await p2.goto("http://localhost:3000/runs", { waitUntil: "networkidle" });
await p2.waitForTimeout(500);
await p2.screenshot({ path: "/tmp/ui8-mobile.png" });
await b.close(); console.log("OK");
