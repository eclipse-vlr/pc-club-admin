import { spawn } from 'node:child_process';
import { createReadStream } from 'node:fs';
import { readFile, readdir, stat } from 'node:fs/promises';
import { createHash, randomUUID } from 'node:crypto';
import { pipeline } from 'node:stream/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// No destination URL is accepted. Restoration only targets this newly created,
// network-isolated container; it cannot replace a database on the club server.
const archive=path.resolve(process.argv[2]??'');
const container='pcclub-restore-check-'+randomUUID();
let created=false;
const run=(args,{input=false,capture=false}={})=>new Promise((resolve,reject)=>{
 const child=spawn('docker',args,{stdio:[input?'pipe':'ignore',capture?'pipe':'inherit','inherit'],timeout:300_000});
 let output='';if(capture)child.stdout.on('data',chunk=>{output+=chunk;});
 child.once('error',reject);
 child.once('close',code=>code===0?resolve(output):reject(new Error('Docker operation failed')));
 if(input)pipeline(createReadStream(archive),child.stdin).catch(error=>{child.kill();reject(error);});
});
try{
 if(process.argv.length!==3||!archive.endsWith('.dump'))throw new Error('Usage: node scripts/verify-backup.mjs backups/<archive>.dump');
 const metadata=JSON.parse(await readFile(archive+'.json','utf8'));
 if(metadata.format!==1||metadata.archive!==path.basename(archive)||metadata.postgres_major!==17||! /^[a-f0-9]{64}$/.test(metadata.sha256))throw new Error('Unsupported backup manifest');
 if(!(await stat(archive)).isFile())throw new Error('Archive is not a file');
 const hash=createHash('sha256');for await(const chunk of createReadStream(archive))hash.update(chunk);
 if(hash.digest('hex')!==metadata.sha256)throw new Error('Archive checksum mismatch');
 await run(['run','--detach','--rm','--name',container,'--network','none','--tmpfs','/var/lib/postgresql/data:rw,nosuid,size=1g','--env','POSTGRES_USER=restorecheck','--env','POSTGRES_DB=restorecheck','--env','POSTGRES_HOST_AUTH_METHOD=trust','postgres:17.6-bookworm'],{capture:true});created=true;
 let ready=false;
 for(let attempt=0;attempt<30;attempt++){
  try{await run(['exec',container,'sh','-c','test "$(head -n 1 /var/lib/postgresql/data/postmaster.pid 2>/dev/null)" = 1 && pg_isready -q -U restorecheck -d restorecheck'],{capture:true});ready=true;break;}catch{await new Promise(resolve=>setTimeout(resolve,1000));}
 }
 if(!ready)throw new Error('Isolated PostgreSQL did not become ready');
 await run(['exec','-i',container,'pg_restore','-U','restorecheck','-d','restorecheck','--exit-on-error','--single-transaction','--no-owner','--no-acl'],{input:true});
 const versions=await run(['exec',container,'psql','-X','-U','restorecheck','-d','restorecheck','-v','ON_ERROR_STOP=1','-At','-F',':','-c','SELECT version,checksum FROM schema_migrations ORDER BY version'],{capture:true});
 const migrationDir=fileURLToPath(new URL('../migrations/',import.meta.url));
 const files=(await readdir(migrationDir)).filter(name=>/^\d{6}_.*\.up\.sql$/.test(name)).sort();
 const expected=[];
 for(const file of files){expected.push(Number(file.slice(0,6))+':'+createHash('sha256').update(await readFile(path.join(migrationDir,file))).digest('hex'));}
 if(versions.trim()!==expected.join('\n'))throw new Error('Restored schema does not match this code version; use the matching release');
 const result=await run(['exec',container,'psql','-X','-U','restorecheck','-d','restorecheck','-v','ON_ERROR_STOP=1','-At','-c',`SELECT 'schema_versions='||count(*) FROM schema_migrations;
 SELECT 'users='||count(*) FROM users;
 SELECT 'shifts='||count(*) FROM shifts;
 SELECT 'audit_events='||count(*) FROM audit_events;
 SELECT 'cash_movements='||count(*) FROM cash_movements;
 SELECT 'opening_cash_kopecks='||COALESCE(sum(opening_cash_kopecks)::text,'0') FROM cash_sessions;
 SELECT 'club_settings='||count(*) FROM club_settings;
 DO $$ BEGIN IF (SELECT count(*) FROM club_settings)<>1 THEN RAISE EXCEPTION 'Missing club settings'; END IF; END $$;`],{capture:true});
 console.log('Isolated restore succeeded. Row counts (no personal data):\n'+result);
}catch(error){console.error('Restore verification failed: '+error.message);process.exitCode=1;}
finally{if(created){try{await run(['rm','--force',container],{capture:true});}catch{console.error('Could not remove isolated restore container '+container);process.exitCode=1;}}}
