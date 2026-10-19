// End-to-end recovery drill using synthetic data in disposable containers only.
import { spawn } from 'node:child_process';
import { readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { createHash, randomUUID } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import assert from 'node:assert/strict';

const root=fileURLToPath(new URL('../',import.meta.url));
const source='pcclub-backup-fixture-'+randomUUID();
let created=false;
const artifacts=[];
const run=(command,args,input)=>new Promise((resolve,reject)=>{
 const child=spawn(command,args,{cwd:root,stdio:['pipe','pipe','pipe'],timeout:300_000});
 let stdout='',stderr='';child.stdout.on('data',chunk=>stdout+=chunk);child.stderr.on('data',chunk=>stderr+=chunk);
 child.once('error',reject);child.once('close',code=>code===0?resolve(stdout):reject(new Error(command+' failed: '+stderr)));
 child.stdin.on('error',()=>{});child.stdin.end(input??'');
});
try{
 await run('docker',['run','--detach','--rm','--name',source,'--network','none','--tmpfs','/var/lib/postgresql/data:rw,nosuid,size=1g','--env','POSTGRES_USER=fixture','--env','POSTGRES_DB=fixture','--env','POSTGRES_HOST_AUTH_METHOD=trust','postgres:17.6-bookworm']);created=true;
 let ready=false;
 for(let attempt=0;attempt<30;attempt++){
  try{await run('docker',['exec',source,'sh','-c','test "$(head -n 1 /var/lib/postgresql/data/postmaster.pid 2>/dev/null)" = 1 && pg_isready -q -U fixture -d fixture']);ready=true;break;}catch{await new Promise(resolve=>setTimeout(resolve,1000));}
 }
 if(!ready)throw new Error('Fixture database not ready');
 const files=(await readdir(path.join(root,'migrations'))).filter(name=>/^\d{6}_.*\.up\.sql$/.test(name)).sort();
 let sql='CREATE TABLE schema_migrations(version integer PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now());\n';
 for(const file of files){
  const body=await readFile(path.join(root,'migrations',file),'utf8');sql+=body+'\n';
  sql+=`INSERT INTO schema_migrations(version,checksum) VALUES(${Number(file.slice(0,6))},'${createHash('sha256').update(body).digest('hex')}');\n`;
 }
 sql+=`INSERT INTO club_settings(id,timezone) VALUES(1,'Europe/Moscow');
 INSERT INTO users(id,display_name) VALUES('00000000-0000-4000-8000-000000000001','Synthetic recovery user');
 INSERT INTO user_roles VALUES('00000000-0000-4000-8000-000000000001','admin');
 INSERT INTO shifts(id,owner_id,business_date,kind,status,planned_start_at,planned_end_at,template_version,created_by)
 VALUES('00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000001','2026-10-19','day','opening','2026-10-19T06:00:00Z','2026-10-19T18:00:00Z',1,'00000000-0000-4000-8000-000000000001');
 INSERT INTO cash_sessions(id,shift_id,opening_cash_kopecks) VALUES('00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000002',9007199254740993);
 INSERT INTO cash_movements(cash_session_id,kind,amount_kopecks,reason,occurred_at,actor_id)
 VALUES('00000000-0000-4000-8000-000000000003','collection',1,'Synthetic recovery collection',now(),'00000000-0000-4000-8000-000000000001');
 INSERT INTO audit_events(actor_id,action,entity_id,request_id,success) VALUES('00000000-0000-4000-8000-000000000001','recovery_fixture','00000000-0000-4000-8000-000000000002','synthetic-recovery',true);`;
 await run('docker',['exec','-i',source,'psql','-X','-U','fixture','-d','fixture','-v','ON_ERROR_STOP=1'],sql);
 const output=await run(process.execPath,['scripts/backup.mjs','--container',source]);
 const match=output.match(/Backup created in (backups\/[^\r\n]+\.dump)/);
 assert.ok(match,'backup path reported');
 const archive=path.join(root,match[1]);artifacts.push(archive,archive+'.json');
 // Change the source after pg_dump: the archived snapshot must remain valid.
 await run('docker',['exec',source,'psql','-X','-U','fixture','-d','fixture','-v','ON_ERROR_STOP=1','-c',"UPDATE users SET display_name='Changed after backup'"]);
 const verified=await run(process.execPath,['scripts/verify-backup.mjs',archive]);
 for(const expected of ['schema_versions='+files.length,'users=1','shifts=1','cash_movements=1','audit_events=1','opening_cash_kopecks=9007199254740993'])assert.ok(verified.includes(expected),expected);
 const corrupt=archive.replace('.dump','-corrupt.dump');artifacts.push(corrupt,corrupt+'.json');
 const metadata=JSON.parse(await readFile(archive+'.json','utf8'));metadata.archive=path.basename(corrupt);
 await writeFile(corrupt,Buffer.from('corrupted synthetic archive'));await writeFile(corrupt+'.json',JSON.stringify(metadata));
 await assert.rejects(run(process.execPath,['scripts/verify-backup.mjs',corrupt]),/checksum mismatch/);
 metadata.sha256=createHash('sha256').update(await readFile(corrupt)).digest('hex');await writeFile(corrupt+'.json',JSON.stringify(metadata));
 await assert.rejects(run(process.execPath,['scripts/verify-backup.mjs',corrupt]),/Restore verification failed/);
 console.log('Recovery drill passed: real dump/restore, schema checksums, row counts, exact money, corruption and invalid archive rejection.');
}catch(error){console.error(error.message);process.exitCode=1;}
finally{
 for(const artifact of artifacts)await rm(artifact,{force:true});
 if(created)try{await run('docker',['rm','--force',source]);}catch{console.error('Could not remove fixture container '+source);process.exitCode=1;}
}
