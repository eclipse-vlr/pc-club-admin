import { readFileSync, writeFileSync } from 'node:fs';
import { randomBytes } from 'node:crypto';
const file = '.env';
let text = readFileSync(file, 'utf8');
const entries = [...text.matchAll(/^PIN_PEPPER=(.*)$/gm)];
if (entries.length > 1) throw new Error('Duplicate PIN_PEPPER: resolve manually');
if (entries.length === 0) text += `\nPIN_PEPPER=${randomBytes(32).toString('hex')}\n`;
else if (entries[0][1].trim() === '') text = text.replace(/^PIN_PEPPER=.*$/m, `PIN_PEPPER=${randomBytes(32).toString('hex')}`);
else { process.stdout.write('PIN_PEPPER уже задан; сохранён без изменений.\n'); process.exit(0); }
writeFileSync(file, text, { mode: 0o600 });
process.stdout.write('PIN_PEPPER создан в .env; не добавляйте файл в Git.\n');
