// Node 22+, Chrome CDP, real API and Vite dev/preview; use a test database only.
// node scripts/attachment-preview-smoke.mjs [9333] [http://127.0.0.1:5173]
// Optional PREVIEW_TEST_USER / PREVIEW_TEST_PASSWORD; creates a test project and attachments.
import assert from 'node:assert/strict';
import { zipSync, strToU8 } from 'fflate';
const [port='9333', origin='http://127.0.0.1:5173'] = process.argv.slice(2);
const page = await (await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' })).json();
if (!page) {
  throw new Error('没有找到 page 类型的调试目标');
}

const ws = new WebSocket(page.webSocketDebuggerUrl);
let nextId = 1;
const pending = new Map();

let exceptions = 0;

ws.addEventListener('message', (event) => {
  const msg = JSON.parse(event.data);
  if (msg.id && pending.has(msg.id)) {
    const { resolve, reject } = pending.get(msg.id);
    pending.delete(msg.id);
    if (msg.error) reject(new Error(JSON.stringify(msg.error)));
    else resolve(msg.result);
  } else if (msg.method) {
    if (msg.method === 'Runtime.exceptionThrown') exceptions++;
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


async function until(expression) {
 for(let i=0;i<100;i++){if(await evaluate(expression))return;await sleep(100)}
 throw new Error('Browser condition did not become true: '+expression);
}
async function navigate(path, text) {
 await send('Page.navigate',{url:origin+path});
 await until(`(document.body?.innerText ?? '').includes(${JSON.stringify(text)})`);
}
const docx = {
  "[Content_Types].xml": "<Types xmlns=\"http://schemas.openxmlformats.org/package/2006/content-types\"><Override PartName=\"/word/document.xml\" ContentType=\"application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml\"/></Types>",
  "_rels/.rels": "<Relationships xmlns=\"http://schemas.openxmlformats.org/package/2006/relationships\"><Relationship Id=\"rId1\" Type=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument\" Target=\"word/document.xml\"/></Relationships>",
  "word/document.xml": "<w:document xmlns:w=\"http://schemas.openxmlformats.org/wordprocessingml/2006/main\"><w:body><w:p><w:r><w:t>DOCX 文字预览 &lt;script&gt;window.previewPwned=true&lt;/script&gt;</w:t></w:r></w:p></w:body></w:document>"
};
const xlsx = {
  "[Content_Types].xml": "<Types xmlns=\"http://schemas.openxmlformats.org/package/2006/content-types\"><Override PartName=\"/xl/workbook.xml\" ContentType=\"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml\"/></Types>",
  "_rels/.rels": "<Relationships xmlns=\"http://schemas.openxmlformats.org/package/2006/relationships\"><Relationship Id=\"rId1\" Type=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument\" Target=\"xl/workbook.xml\"/></Relationships>",
  "xl/workbook.xml": "<workbook xmlns=\"http://schemas.openxmlformats.org/spreadsheetml/2006/main\" xmlns:r=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships\"><sheets><sheet name=\"计划\" sheetId=\"1\" r:id=\"rId1\"/><sheet name=\"汇总\" sheetId=\"2\" r:id=\"rId2\"/></sheets></workbook>",
  "xl/_rels/workbook.xml.rels": "<Relationships xmlns=\"http://schemas.openxmlformats.org/package/2006/relationships\"><Relationship Id=\"rId1\" Type=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet\" Target=\"worksheets/sheet1.xml\"/><Relationship Id=\"rId2\" Type=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet\" Target=\"worksheets/sheet2.xml\"/></Relationships>",
  "xl/worksheets/sheet1.xml": "<worksheet xmlns=\"http://schemas.openxmlformats.org/spreadsheetml/2006/main\"><sheetData><row r=\"1\"><c r=\"A1\" t=\"inlineStr\"><is><t>工作表一</t></is></c><c r=\"B1\"><v>50</v></c></row></sheetData></worksheet>",
  "xl/worksheets/sheet2.xml": "<worksheet xmlns=\"http://schemas.openxmlformats.org/spreadsheetml/2006/main\"><sheetData><row r=\"1\"><c r=\"A1\" t=\"inlineStr\"><is><t>工作表二</t></is></c><c r=\"B1\"><v>50</v></c></row></sheetData></worksheet>"
};

function makePdf() {
 const stream = 'BT /F1 24 Tf 40 700 Td (Attachment preview) Tj ET';
 const objects = ['<< /Type /Catalog /Pages 2 0 R >>', '<< /Type /Pages /Kids [3 0 R] /Count 1 >>', '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>', '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>', `<< /Length ${stream.length} >>\nstream\n${stream}\nendstream`];
 let out = '%PDF-1.4\n'; const offsets = [];
 objects.forEach((object, i) => { offsets.push(out.length); out += `${i + 1} 0 obj\n${object}\nendobj\n`; });
 const xref = out.length;
 return strToU8(out + `xref\n0 6\n0000000000 65535 f \n${offsets.map(offset => String(offset).padStart(10, '0') + ' 00000 n \n').join('')}trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF`);
}
function office(entries) { return zipSync(Object.fromEntries(Object.entries(entries).map(([name, text]) => [name, strToU8(text)]))); }
async function post(path, data) {
 const result = await evaluate(`(async()=>{const r=await fetch('/api/v1'+${JSON.stringify(path)},{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(${JSON.stringify(data)})});return {status:r.status,body:r.status===204?null:await r.json()}})()`);
 assert.ok(result.status < 300, path+': '+JSON.stringify(result)); return result.body?.data;
}
try {
 await send('Runtime.enable'); await send('Page.enable'); await send('Page.bringToFront');
 await navigate('/login', '登录');
 await post('/auth/login', {identifier:process.env.PREVIEW_TEST_USER || 'bootstrap_admin',password:process.env.PREVIEW_TEST_PASSWORD || 'bootstrap_admin_test_password'});
 const group = await post('/groups', {name:'附件预览冒烟-'+Date.now()});
 const project = await post('/projects', {name:'附件预览冒烟',group_id:group.id,start_date:'2026-10-23',end_date:'2026-10-30'});
 const ids = {};
 const files = {'notes.txt':strToU8('统一预览验收 <script>window.previewPwned=true</script>'),'sample.pdf':makePdf(),'sample.docx':office(docx),'sample.xlsx':office(xlsx),'unsafe.html':strToU8('<script>window.previewPwned=true</script>')};
 for(const [name,bytes] of Object.entries(files)) {
  const result = await evaluate(`(async()=>{const f=new FormData();f.append('file',new Blob([new Uint8Array(${JSON.stringify([...bytes])})]),${JSON.stringify(name)});const r=await fetch('/api/v1/projects/${project.id}/attachments',{method:'POST',body:f});return {status:r.status,body:r.status===204?null:await r.json()}})()`);
  assert.equal(result.status,201,name+': '+JSON.stringify(result)); ids[name]=result.body.data.id;
 }
 await navigate('/projects/'+project.id,'附件预览冒烟');
 await until('!!document.querySelector(\'[aria-label="预览 notes.txt"]\')');
 await evaluate('document.querySelector(\'[aria-label="预览 notes.txt"]\').click()');
 await until("document.querySelector('.MuiDialog-paper[role=dialog] pre')?.textContent.includes('统一预览验收')");
 await send('Input.dispatchMouseEvent',{type:'mousePressed',x:1,y:1,button:'left',clickCount:1});
 await send('Input.dispatchMouseEvent',{type:'mouseReleased',x:1,y:1,button:'left',clickCount:1});
 await send('Input.dispatchKeyEvent',{type:'keyDown',key:'Escape',code:'Escape',windowsVirtualKeyCode:27});
 await send('Input.dispatchKeyEvent',{type:'keyUp',key:'Escape',code:'Escape',windowsVirtualKeyCode:27});
 await sleep(400); assert.ok(await evaluate("!!document.querySelector('.MuiDialog-paper[role=dialog]')"));
 assert.equal(await evaluate('window.previewPwned'),undefined);
 const link=await evaluate("(()=>{const a=document.querySelector('[role=dialog] a[target=_blank]');return {href:a?.getAttribute('href'),rel:a?.rel}})()");
 assert.equal(link.href,'/attachments/'+ids['notes.txt']+'/preview'); assert.ok(link.rel.includes('noopener'));
 const closePoint = await evaluate('(()=>{const r=document.querySelector(\'[aria-label="关闭预览"]\').getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()');
 await send('Input.dispatchMouseEvent',{type:'mouseMoved',...closePoint});
 await send('Input.dispatchMouseEvent',{type:'mousePressed',...closePoint,button:'left',clickCount:1});
 await send('Input.dispatchMouseEvent',{type:'mouseReleased',...closePoint,button:'left',clickCount:1}); await until("!document.querySelector('.MuiDialog-paper[role=dialog]')");
 console.log('PASS modal text, backdrop/Escape, explicit exit and new-tab link');
 for(const [name,condition] of [
  ['sample.pdf',"!!document.querySelector('canvas[aria-label]')?.width && !document.querySelector('[aria-label=\"正在显示 PDF\"]')"],
  ['sample.docx',"document.querySelector('pre')?.textContent.includes('DOCX 文字预览')"],
  ['sample.xlsx',"document.querySelector('table')?.textContent.includes('工作表一')"],
  ['unsafe.html',"document.body.innerText.includes('暂不支持此格式')"],
 ]) {
  await send('Page.navigate',{url:origin+'/attachments/'+ids[name]+'/preview'}); await until(condition);
  assert.equal(await evaluate('window.previewPwned'),undefined); console.log('PASS '+name);
 }
 await post('/auth/logout',{});
 await send('Page.navigate',{url:origin+'/attachments/'+ids['notes.txt']+'/preview'}); await until("location.pathname === '/login'");
 assert.equal(exceptions,0); console.log('PASS authentication guard; zero runtime exceptions');
} finally {
 ws.close(); await fetch(`http://127.0.0.1:${port}/json/close/${page.id}`);
}
