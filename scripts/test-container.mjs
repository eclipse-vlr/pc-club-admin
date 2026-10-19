import { spawn } from 'node:child_process';
import { randomUUID,randomBytes } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import assert from 'node:assert/strict';
const root=fileURLToPath(new URL('../',import.meta.url));
const prefix='pcclub-check-'+randomUUID();
const network=prefix,db=prefix+'-db';
const containers=[];let createdNetwork=false;
const password=randomBytes(24).toString('hex');
const run=(args,env=process.env)=>new Promise((resolve,reject)=>{
 const child=spawn('docker',args,{cwd:root,env,stdio:['ignore','pipe','pipe'],timeout:120000});
 let output='',error='';child.stdout.on('data',c=>output+=c);child.stderr.on('data',c=>error+=c);
 child.once('error',reject);child.once('close',code=>code===0?resolve(output.trim()):reject(new Error('Docker smoke failed: '+error)));
});
try{
 await run(['compose','--env-file','deploy/.env.example','-f','deploy/compose.yaml','config','--quiet'],{...process.env,CLUB_DOMAIN:'club.example.invalid',POSTGRES_USER:'fixture',POSTGRES_DB:'fixture',POSTGRES_PASSWORD:password,SERVER_DATABASE_URL:'postgres://fixture:'+password+'@db/fixture?sslmode=disable',PIN_PEPPER:'synthetic-image-pepper-at-least-32-characters'});
 await run(['run','--rm','--network','none','-e','CLUB_DOMAIN=club.example.invalid','-v',path.join(root,'deploy/Caddyfile')+':/etc/caddy/Caddyfile:ro','caddy:2.11.2-alpine','caddy','validate','--config','/etc/caddy/Caddyfile','--adapter','caddyfile']);
 await run(['network','create','--internal',network]);createdNetwork=true;
 await run(['run','-d','--rm','--name',db,'--network',network,'--tmpfs','/var/lib/postgresql/data:rw,nosuid,size=1g','-e','POSTGRES_USER=fixture','-e','POSTGRES_DB=fixture','-e','POSTGRES_PASSWORD='+password,'postgres:17.6-bookworm']);containers.push(db);
 let ready=false;
 for(let i=0;i<30;i++){try{await run(['exec',db,'sh','-c','test "$(head -n 1 /var/lib/postgresql/data/postmaster.pid 2>/dev/null)" = 1 && pg_isready -q -U fixture -d fixture']);ready=true;break;}catch{await new Promise(r=>setTimeout(r,1000));}}
 assert.ok(ready,'isolated database ready');
 const image=process.env.PC_CLUB_TEST_IMAGE||'pc-club-admin:local';
 const environment=['-e','DATABASE_URL=postgres://fixture:'+password+'@'+db+'/fixture?sslmode=disable','-e','PIN_PEPPER=synthetic-image-pepper-at-least-32-characters','-e','CLUB_TIMEZONE=Europe/Moscow','-e','CASH_POLICY_STATUS=unverified','-e','DRIVE_EXCEL_SYNC=disabled'];
 await run(['run','--rm','--network',network,...environment,'-e','APP_ENV=test','-e','APP_ORIGIN=http://127.0.0.1:8080','--entrypoint','/app/migrate',image,'up']);
 // Check the packaged bootstrap CLI with an isolated synthetic secret pipe.
 const bootstrap=spawn('docker',['run','--rm','-i','--network',network,...environment,'-e','APP_ENV=test','-e','APP_ORIGIN=http://127.0.0.1:8080','--entrypoint','/app/admin',image,'bootstrap-manager','--login','manager','--name','Synthetic image manager','--password-stdin'],{stdio:['pipe','pipe','pipe'],timeout:30000});
 const bootstrapped=new Promise((resolve,reject)=>{let error='';bootstrap.stderr.on('data',chunk=>error+=chunk);bootstrap.stdout.resume();bootstrap.once('error',reject);bootstrap.once('close',code=>code===0?resolve():reject(new Error('Packaged bootstrap CLI failed: '+error)));});
 bootstrap.stdin.on('error',()=>{});bootstrap.stdin.end('synthetic-image-manager-password\n');await bootstrapped;
 for(const mode of ['test','production']){
  const api=prefix+'-'+mode;
  await run(['run','-d','--rm','--name',api,'--network',network,'--read-only','--tmpfs','/tmp','--cap-drop','ALL','--security-opt','no-new-privileges:true',...environment,'-e','APP_ENV='+mode,'-e','APP_ORIGIN='+(mode==='test'?'http://127.0.0.1:8080':'https://club.example.invalid'),image]);containers.push(api);
  assert.equal(await run(['inspect','--format','{{.Config.User}}',api]),'10001:10001');
  // A node probe joins only the disposable internal network. No test service
  // publishes a host port, so this drill cannot collide with the club API.
  const probe=`const base='http://'+process.argv[1]+':8080';
   let ready=false,failure="";
   for(let i=0;i<30;i++){try{if((await fetch(base+'/healthz',{signal:AbortSignal.timeout(2000)})).status===200){ready=true;break;}}catch(error){failure=error.cause?.code??error.message;}await new Promise(r=>setTimeout(r,500));}
   if(!ready)throw new Error('API did not start: '+failure);
   const response=await fetch(base+'/readyz',{signal:AbortSignal.timeout(3000)});
   if(response.status!==(process.argv[2]==='test'?200:503))throw new Error('Readiness status');
   if(process.argv[2]==='production'&&!(await response.text()).includes('CASH_POLICY_UNVERIFIED'))throw new Error('Production gate missing');
   if(!(await (await fetch(base+'/')).text()).includes('lang="ru"'))throw new Error('Static UI missing');`;
  await run(['run','--rm','--network',network,'node:24.19.0-bookworm-slim','node','--input-type=module','-e',probe,api,mode]);
  await run(['exec',api,'/app/healthcheck']);
  await run(['rm','-f',api]);containers.pop();
 }
 console.log('Container smoke passed: Compose/Caddy config, migrations, non-root read-only API, static UI, healthcheck, test readiness and production block.');
}catch(error){console.error(error.message);for(const name of containers.filter(name=>name!==db)){try{console.error(await run(['logs','--tail','12',name]));}catch{}}process.exitCode=1;}
finally{
 for(const container of containers.reverse()){try{await run(['rm','-f',container]);}catch{process.exitCode=1;}}
 if(createdNetwork)try{await run(['network','rm',network]);}catch{process.exitCode=1;}
}
