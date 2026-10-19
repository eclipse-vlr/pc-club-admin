/* global document */
import process from 'node:process';
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
 await prepare.locator('select[name="receiver"]').selectOption(process.env.PC_CLUB_E2E_RECEIVER);
 await prepare.getByLabel('Мой личный PIN',{exact:true}).fill('123456');
 await prepare.getByRole('button').click();
 await page.getByRole('heading',{level:1,name:'Вход в приложение'}).waitFor();
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
 await page.getByLabel('Мой личный PIN',{exact:true}).fill('654321');
 await page.getByRole('button',{name:'Лично принять смену',exact:true}).click();
 await page.getByRole('alert').filter({hasText:'Ваш пересчёт не совпадает'}).waitFor();
 await page.getByRole('button',{name:'Проверить сессию и перечитать передачу',exact:true}).click();
 await page.getByRole('alert').waitFor({state:'hidden'});
 await page.getByLabel('Мой пересчёт наличных · рубли',{exact:true}).waitFor();
 await count('15000');
 // Simulate a lost success response. Reload reconciles the committed operation.
 await page.route('**/api/v1/handovers/*/accept',async route=>{await route.fetch();await route.abort('connectionreset');});
 await page.getByLabel('Мой личный PIN',{exact:true}).fill('654321');
 await page.getByRole('button',{name:'Лично принять смену',exact:true}).click();
 await page.getByRole('alert').waitFor();
 await page.unroute('**/api/v1/handovers/*/accept');
 await page.reload();
 await page.getByRole('region',{name:'Текущая смена',exact:true}).getByText('Активная смена',{exact:false}).waitFor();
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
 await managerPage.route('**/api/v1/manager/dashboard',route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{code:'SERVICE_UNAVAILABLE',message:'Синтетический сбой связи'}})}));
 await dashboard.getByRole('button',{name:'Обновить панель',exact:true}).click();
 await dashboard.getByRole('alert').filter({hasText:'Ниже сохранённый снимок; он может быть неактуален'}).waitFor();
 await dashboard.getByRole('heading',{name:'Ответственный: Принимающий E2E',exact:true}).waitFor();
 await managerContext.close();
 assert.equal(scriptErrors.length,0,'no browser script errors');
 await context.close();
}finally{await browser.close();}
