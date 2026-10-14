// 浏览器级冒烟：用 Chrome DevTools Protocol 在真实浏览器里走一遍
// 登录页 → 填表提交 → 概览页 → 截图（宽视口 + 窄视口）。
// 它验证的是"前端在浏览器里真的能跑"，不是接口契约——接口本身用 backend/scripts/smoke.ps1。
//
// 前置（见 frontend/docs/F03 §7）：
//   1) 后端已启动并监听 127.0.0.1:8000（backend: make run-test-config 或等价命令）
//   2) 前端开发服务器已启动：pnpm dev（监听 127.0.0.1:5173）
//   3) 已启动一个带远程调试端口的 Chrome：
//      chrome --headless=new --remote-debugging-port=9333 --user-data-dir=%TEMP%\pb-chrome about:blank
//
// 用法：node scripts/browser-smoke.mjs <cdpPort> <appUrl> <identifier> <password> <outPng>
// 例：  node scripts/browser-smoke.mjs 9333 http://127.0.0.1:5173/login bootstrap_admin <密码> .verify/dashboard.png

const [port, appUrl, identifier, password, outPng] = process.argv.slice(2);

const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
const page = targets.find((t) => t.type === 'page');
if (!page) {
  throw new Error('没有找到 page 类型的调试目标');
}

const ws = new WebSocket(page.webSocketDebuggerUrl);
let nextId = 1;
const pending = new Map();
const events = [];

ws.addEventListener('message', (event) => {
  const msg = JSON.parse(event.data);
  if (msg.id && pending.has(msg.id)) {
    const { resolve, reject } = pending.get(msg.id);
    pending.delete(msg.id);
    if (msg.error) reject(new Error(JSON.stringify(msg.error)));
    else resolve(msg.result);
  } else if (msg.method) {
    events.push(msg.method);
  }
});

await new Promise((resolve, reject) => {
  ws.addEventListener('open', resolve, { once: true });
  ws.addEventListener('error', reject, { once: true });
});

const send = (method, params = {}) =>
  new Promise((resolve, reject) => {
    const id = nextId++;
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params }));
  });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function evaluate(expression) {
  const result = await send('Runtime.evaluate', {
    expression,
    awaitPromise: true,
    returnByValue: true,
  });
  if (result.exceptionDetails) {
    throw new Error(`页面内异常: ${JSON.stringify(result.exceptionDetails)}`);
  }
  return result.result.value;
}

await send('Page.enable');
await send('Runtime.enable');
await send('Log.enable');

console.log('→ 打开', appUrl);
await send('Page.navigate', { url: appUrl });
await sleep(3000);

const loginVisible = await evaluate(`document.body.innerText.includes('登录')`);
console.log('  登录页可见:', loginVisible);

// React 受控输入：必须用原生 setter + input 事件，直接赋值不会触发 onChange
const fillResult = await evaluate(`(() => {
  const setValue = (el, value) => {
    const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), 'value').set;
    setter.call(el, value);
    el.dispatchEvent(new Event('input', { bubbles: true }));
  };
  const idInput = document.querySelector('input#identifier');
  const pwInput = document.querySelector('input#password');
  if (!idInput || !pwInput) return 'inputs-not-found';
  setValue(idInput, ${JSON.stringify(identifier)});
  setValue(pwInput, ${JSON.stringify(password)});
  const form = idInput.closest('form');
  form.requestSubmit();
  return 'submitted';
})()`);
console.log('  提交表单:', fillResult);

// 等待登录完成并进入概览页
let text = '';
for (let i = 0; i < 20; i += 1) {
  await sleep(500);
  text = await evaluate('document.body.innerText');
  if (text.includes('当前登录用户')) break;
}
console.log('→ 概览页可见:', text.includes('当前登录用户'));
console.log('--- 页面文本（前 700 字）---');
console.log(text.slice(0, 700));

const shot = await send('Page.captureScreenshot', { format: 'png' });
const { writeFileSync } = await import('node:fs');
writeFileSync(outPng, Buffer.from(shot.data, 'base64'));
console.log('→ 截图已保存:', outPng);

// 窄视口：验证响应式侧边栏（模板的三视口逻辑：xs 用 temporary drawer）
await send('Emulation.setDeviceMetricsOverride', {
  width: 480,
  height: 900,
  deviceScaleFactor: 1,
  mobile: true,
});
await sleep(1200);
const narrow = await send('Page.captureScreenshot', { format: 'png' });
writeFileSync(outPng.replace('.png', '-narrow.png'), Buffer.from(narrow.data, 'base64'));
console.log('→ 窄视口截图已保存:', outPng.replace('.png', '-narrow.png'));

ws.close();
