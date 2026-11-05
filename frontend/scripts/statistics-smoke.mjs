// P04 v2: run the browser against the real PostgreSQL/Redis HTTP harness.
// This uses the repository's isolated test database and cleans its test fixtures.
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
const result=spawnSync('go',['test','./internal/testsupport','-run','^TestInsightsBrowser$','-count=1','-v'],{
 cwd:fileURLToPath(new URL('../../backend',import.meta.url)),env:{...process.env,INSIGHTS_BROWSER:'1'},stdio:'inherit',windowsHide:true});
if(result.error)throw result.error;process.exit(result.status??1);
