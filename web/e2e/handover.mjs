/* global document */
import process from 'node:process';
import { Buffer } from 'node:buffer';
import assert from 'node:assert/strict';
import { chromium } from 'playwright-core';

const url=process.env.PC_CLUB_E2E_URL;
assert.ok(url?.startsWith('http://127.0.0.1:')||url?.startsWith('http://[::1]:'),'isolated loopback server required');
const browser=await chromium.launch({executablePath:process.env.PC_CLUB_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox']});
try{
 const context=await browser.newContext({viewport:{width:1280,height:900}});
 await context.addCookies([{name:'pcclub_device',value:process.env.PC_CLUB_E2E_DEVICE,url,httpOnly:true,sameSite:'Lax'}]);
 const page=await context.newPage();
 page.setDefaultTimeout(12000);
 const scriptErrors=[];page.on('pageerror',error=>scriptErrors.push(error.message));
 await page.goto(url);
 // A separate remote manager context has no trusted-device cookie.
 const managerContext=await browser.newContext({viewport:{width:1280,height:900}});
 const managerPage=await managerContext.newPage();managerPage.setDefaultTimeout(12000);
 managerPage.on('pageerror',error=>scriptErrors.push(error.message));
 await managerPage.goto(url);
 await managerPage.getByRole('button',{name:'Управляющий · пароль',exact:true}).click();
 await managerPage.getByLabel('Логин управляющего',{exact:true}).fill('manager');
 await managerPage.getByLabel('Пароль',{exact:true}).fill(process.env.PC_CLUB_E2E_MANAGER_PASSWORD);
 await managerPage.getByRole('button',{name:'Войти как управляющий',exact:true}).click();
 const dashboard=managerPage.getByRole('region',{name:'Панель руководителя',exact:true});
 await dashboard.getByRole('heading',{name:'Ответственный: Сдающий E2E',exact:true}).waitFor();
 assert.equal((await managerContext.cookies()).some(cookie=>cookie.name.endsWith('pcclub_device')),false);
 async function refreshDashboard(){
  const updated=managerPage.waitForResponse(response=>response.url().endsWith('/manager/dashboard')&&response.status()===200);
  await dashboard.getByRole('button',{name:'Обновить панель',exact:true}).click();await updated;
 }

 async function login(user,pin,name){
  await page.locator('select[name="user"]').selectOption(user);
  await page.getByLabel('Личный PIN',{exact:true}).fill(pin);
  await page.getByRole('button',{name:'Войти по PIN',exact:true}).click();
  await page.getByRole('heading',{level:1,name}).waitFor();
 }
 await login(process.env.PC_CLUB_E2E_OWNER,'123456','Сдающий E2E');
 assert.equal(await page.getByRole('region',{name:'Панель руководителя',exact:true}).count(),0);
 const prepare=page.locator('form').filter({has:page.getByRole('button',{name:'Подготовить передачу и выйти',exact:true})});
 // The preparation commits and revokes the sender session, but its response is lost.
 let preparationCalls=0;
 await page.route('**/api/v1/shifts/*/handovers',async route=>{
  preparationCalls++;
  const response=await route.fetch();assert.equal(response.status(),201);
  await route.abort('connectionreset');
 });
 await prepare.locator('select[name="receiver"]').selectOption(process.env.PC_CLUB_E2E_RECEIVER);
 await prepare.getByLabel('Мой личный PIN',{exact:true}).fill('123456');
 await prepare.getByRole('button',{name:'Подготовить передачу и выйти',exact:true}).click();
 const transferPanel=page.getByRole('region',{name:'Передача смены',exact:true});
 await transferPanel.getByRole('alert').filter({hasText:'Результат действия неизвестен'}).waitFor();
 assert.ok(await prepare.getByRole('button',{name:'Подготовить передачу и выйти',exact:true}).isDisabled());
 await transferPanel.getByRole('button',{name:'Проверить сессию и перечитать передачу',exact:true}).click();
 await page.getByRole('heading',{level:1,name:'Вход в приложение'}).waitFor();
 await page.unroute('**/api/v1/shifts/*/handovers');
 assert.equal(preparationCalls,1,'unknown preparation must not be resubmitted');
 assert.equal((await context.request.get(url+'/api/v1/auth/me')).status(),401,'sender session must end');
 await refreshDashboard();
 await dashboard.getByText('Принимающий: Принимающий E2E.',{exact:false}).waitFor();
 await dashboard.getByRole('heading',{name:'Ответственный: Сдающий E2E',exact:true}).waitFor();
 await login(process.env.PC_CLUB_E2E_RECEIVER,'654321','Принимающий E2E');
 await page.getByText('Редакция 1 · Ожидает приёма',{exact:true}).waitFor();
 assert.ok(await page.getByText('Поломка зафиксирована, ремонт не завершён',{exact:false}).count()>0);
 for(let i=0;i<12;i++){
  const entry=page.locator('form.checklist-item').nth(i);
  await entry.locator('select[name="answer"]').selectOption('yes');
  const saved=page.waitForResponse(response=>response.url().includes('/checklist/')&&response.request().method()==='PATCH'&&response.status()===200);
  await entry.getByRole('button',{name:'Сохранить ответ',exact:true}).click();await saved;
  await page.waitForFunction(index=>document.querySelectorAll('form.checklist-item')[index]?.textContent?.includes('Сохранено'),i);
 }
 async function count(amount){
  await page.getByLabel('Мой пересчёт наличных · рубли',{exact:true}).fill(amount);
  const saved=page.waitForResponse(response=>response.url().endsWith('/receipt')&&response.request().method()==='PATCH'&&response.status()===200);
  await page.getByRole('button',{name:'Сохранить мой пересчёт',exact:true}).click();await saved;
  await page.waitForFunction(()=>document.querySelector('input[name="counted"]')?.value==='');
  await page.getByText('Сохранённый пересчёт получателя:',{exact:false}).waitFor();
  await page.getByRole('button',{name:'Сохранить мой пересчёт',exact:true}).waitFor();
 }
 await count('14999,99');
 const acknowledgement=page.getByLabel('ПК E2E:',{exact:false});
 await acknowledgement.check();
 await page.getByRole('button',{name:'Сохранить ознакомление',exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('input[name="notes"]')?.disabled===true);
 const acceptance=page.getByRole('button',{name:'Лично принять смену',exact:true});
 await page.getByText('Ваш пересчёт не совпадает',{exact:false}).waitFor();
 assert.ok(await acceptance.isDisabled(),'one kopeck blocks the UI; server rejection is covered by integration tests');
 await count('15000');
 // Failure before the request reaches the server must preserve the pending transfer.
 let acceptCalls=0;
 await page.route('**/api/v1/handovers/*/accept',async route=>{acceptCalls++;await route.abort('connectionreset');});
 await page.getByLabel('Мой личный PIN',{exact:true}).fill('654321');
 await acceptance.click();
 await transferPanel.getByRole('alert').filter({hasText:'Результат действия неизвестен'}).waitFor();
 assert.ok(await acceptance.isDisabled());
 await page.unroute('**/api/v1/handovers/*/accept');
 await transferPanel.getByRole('button',{name:'Проверить сессию и перечитать передачу',exact:true}).click();
 await transferPanel.getByRole('alert').waitFor({state:'hidden'});
 await page.getByText('Редакция 1 · Ожидает приёма',{exact:true}).waitFor();
 assert.ok(await acceptance.isEnabled());
 await refreshDashboard();
 await dashboard.getByRole('heading',{name:'Ответственный: Сдающий E2E',exact:true}).waitFor();
 assert.equal(acceptCalls,1);
 // Simulate a lost success response. Reload reconciles the committed operation.
 await page.route('**/api/v1/handovers/*/accept',async route=>{acceptCalls++;const response=await route.fetch();assert.equal(response.status(),200);await route.abort('connectionreset');});
 await page.getByLabel('Мой личный PIN',{exact:true}).fill('654321');
 await page.getByRole('button',{name:'Лично принять смену',exact:true}).click();
 await transferPanel.getByRole('alert').filter({hasText:'Результат действия неизвестен'}).waitFor();
 assert.ok(await acceptance.isDisabled());
 await page.unroute('**/api/v1/handovers/*/accept');
 await transferPanel.getByRole('button',{name:'Проверить сессию и перечитать передачу',exact:true}).click();
 await page.getByRole('region',{name:'Текущая смена',exact:true}).getByText('Активная смена',{exact:false}).waitFor();
 await page.reload();
 await page.getByRole('region',{name:'Текущая смена',exact:true}).getByText('Активная смена',{exact:false}).waitFor();
 assert.equal(acceptCalls,2,'one failed request and one committed request, with no automatic replay');
 assert.equal(await page.getByRole('button',{name:'Лично принять смену',exact:true}).count(),0);
 // The recipient can inspect the closed outgoing shift and its frozen transfer.
 await page.locator('summary').filter({hasText:'История смен'}).click();
 await page.locator('select[name="status"]').selectOption('closed');
 await page.getByRole('button',{name:'Показать историю',exact:true}).click();
 await page.getByRole('button',{name:'Просмотреть смену 2026-10-19 · Сдающий E2E · День',exact:true}).click();
 const detail=page.getByRole('region',{name:'Детали смены',exact:true});
 await detail.getByRole('button',{name:'Просмотреть передачу · редакция 1',exact:true}).click();
 const snapshot=page.getByRole('region',{name:'Снимок передачи',exact:true});
 await snapshot.getByRole('heading',{name:'Снимок передачи · Принята · редакция 1',exact:true}).waitFor();
 await snapshot.getByText('Поломка зафиксирована, ремонт не завершён',{exact:false}).waitFor();
 assert.equal(await detail.locator('input,select,textarea').count(),0,'history is read-only');
 await refreshDashboard();
 await dashboard.getByRole('heading',{name:'Ответственный: Принимающий E2E',exact:true}).waitFor();
 // The manager reads audit metadata remotely; club admins have no journal UI.
 assert.equal(await page.locator('summary').filter({hasText:'Журнал действий'}).count(),0);
 await managerPage.locator('summary').filter({hasText:'Журнал действий'}).click();
 const audit=managerPage.getByRole('region',{name:'Журнал действий',exact:true});
 await audit.getByText('Событий на странице:',{exact:false}).waitFor();
 const firstAuditEvent=await audit.locator('ol li summary').first().textContent();
 await audit.getByRole('button',{name:'Следующие события',exact:true}).click();
 await audit.getByRole('button',{name:'Предыдущие события',exact:true}).waitFor();
 assert.notEqual(await audit.locator('ol li summary').first().textContent(),firstAuditEvent);
 await audit.getByRole('button',{name:'Предыдущие события',exact:true}).click();
 await audit.getByText(firstAuditEvent,{exact:true}).waitFor();
 await audit.getByLabel('Действие (точный код)',{exact:true}).fill('handover_accepted');
 await audit.getByRole('button',{name:'Показать журнал',exact:true}).click();
 await audit.getByText('Событий на странице: 1',{exact:true}).waitFor();
 await audit.locator('summary').first().click();
 await audit.getByText('handover_accepted',{exact:true}).waitFor();
 assert.equal(await audit.getByRole('button',{name:'Следующие события',exact:true}).isDisabled(),true);
 await audit.getByLabel('Действие (точный код)',{exact:true}).fill('synthetic_no_such_action');
 await audit.getByRole('button',{name:'Показать журнал',exact:true}).click();
 await audit.getByText('По выбранным фильтрам событий нет.',{exact:true}).waitFor();
 await managerPage.route('**/api/v1/audit-events?**',route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{code:'SERVICE_UNAVAILABLE',message:'Синтетический сбой журнала'}})}));
 await audit.getByRole('button',{name:'Обновить журнал',exact:true}).click();
 await audit.getByRole('alert').filter({hasText:'Синтетический сбой журнала'}).waitFor();
 assert.equal(await audit.getByText('По выбранным фильтрам событий нет.',{exact:true}).count(),0,'failed request is not an empty successful result');
 await managerPage.unroute('**/api/v1/audit-events?**');
 await audit.getByRole('button',{name:'Обновить журнал',exact:true}).click();
 await audit.getByText('По выбранным фильтрам событий нет.',{exact:true}).waitFor();
 // Real downloads use the authenticated manager session and server filters.
 assert.equal(await page.locator('summary').filter({hasText:'Выгрузки CSV'}).count(),0);
 await managerPage.locator('summary').filter({hasText:'Выгрузки CSV'}).click();
 const exportsPanel=managerPage.getByRole('region',{name:'Выгрузки CSV',exact:true});
 await exportsPanel.getByLabel('Дата смены от',{exact:true}).fill('2026-10-19');
 await exportsPanel.getByLabel('Дата смены до',{exact:true}).fill('2026-10-19');
 for(const kind of ['shifts','cash']){
  await exportsPanel.getByLabel('Вид выгрузки',{exact:true}).selectOption(kind);
  const downloading=managerPage.waitForEvent('download');
  await exportsPanel.getByRole('button',{name:'Скачать CSV',exact:true}).click();
  const download=await downloading;
  assert.equal(download.suggestedFilename(),kind+'.csv');
  const stream=await download.createReadStream();const chunks=[];
  for await(const chunk of stream)chunks.push(chunk);
  const csv=Buffer.concat(chunks).toString('utf8');
  assert.ok(csv.includes('unverified_test_only'));
  assert.ok(csv.includes('Сдающий E2E'));
  if(kind==='cash')assert.ok(csv.includes('15000.00'));
  await exportsPanel.getByText('Файл передан браузеру для скачивания.',{exact:true}).waitFor();
 }
 await managerPage.route('**/api/v1/manager/dashboard',route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{code:'SERVICE_UNAVAILABLE',message:'Синтетический сбой связи'}})}));
 await dashboard.getByRole('button',{name:'Обновить панель',exact:true}).click();
 await dashboard.getByRole('alert').filter({hasText:'Ниже сохранённый снимок; он может быть неактуален'}).waitFor();
 await dashboard.getByRole('heading',{name:'Ответственный: Принимающий E2E',exact:true}).waitFor();
 await managerContext.close();
 assert.equal(scriptErrors.length,0,'no browser script errors');
 await context.close();
}finally{await browser.close();}
