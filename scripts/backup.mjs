import { spawn } from 'node:child_process';
import { createReadStream, createWriteStream } from 'node:fs';
import { chmod, mkdir, rename, rm, writeFile } from 'node:fs/promises';
import { createHash, randomUUID } from 'node:crypto';
import { pipeline } from 'node:stream/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const args=process.argv.slice(2);
if(args.length!==0&&(args.length!==2||args[0]!=='--container'||! /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/.test(args[1]))){console.error('Usage: node scripts/backup.mjs [--container NAME]');process.exit(1);}
const source=args.length?['exec',args[1]]:['compose','exec','-T','db'];
const root=fileURLToPath(new URL('../',import.meta.url));
const directory=path.join(root,'backups');
const name='pc-club-'+new Date().toISOString().replaceAll(':','-')+'-'+randomUUID();
const partial=path.join(directory,name+'.dump.partial');
const destination=path.join(directory,name+'.dump');
let published=false;
try {
 await mkdir(directory,{recursive:true,mode:0o700});
 await chmod(directory,0o700);
 const child=spawn('docker',[...source,'sh','-c','case "$(pg_dump --version)" in "pg_dump (PostgreSQL) 17."*) ;; *) echo "PostgreSQL 17 is required" >&2; exit 1 ;; esac; exec pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom --no-owner --no-acl'],{cwd:root,stdio:['ignore','pipe','inherit'],timeout:300_000});
 const completed=new Promise((resolve,reject)=>{child.once('error',reject);child.once('close',code=>code===0?resolve():reject(new Error('pg_dump failed')));});
 await Promise.all([completed,pipeline(child.stdout,createWriteStream(partial,{flags:'wx',mode:0o600}))]).catch(error=>{child.kill();throw error;});
 const hash=createHash('sha256');
 for await(const chunk of createReadStream(partial))hash.update(chunk);
 const metadata={format:1,archive:path.basename(destination),sha256:hash.digest('hex'),created_at:new Date().toISOString(),postgres_major:17,configuration_included:false};
 await writeFile(destination+'.json',JSON.stringify(metadata,null,2)+'\n',{flag:'wx',mode:0o600});
 await rename(partial,destination);published=true;
 console.log('Backup created in backups/'+path.basename(destination));
 console.log('Contains club data and authentication hashes. Store an encrypted copy separately. Configuration and PIN_PEPPER must be secured separately.');
}catch(error){
 console.error('Backup failed: '+error.message);process.exitCode=1;
}finally{
 await rm(partial,{force:true});
 if(!published)await rm(destination+'.json',{force:true});
}
