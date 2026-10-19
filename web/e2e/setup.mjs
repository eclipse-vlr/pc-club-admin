/* global document, window */
import process from 'node:process';
import assert from 'node:assert/strict';
import { chromium } from 'playwright-core';
const url=process.env.PC_CLUB_E2E_URL;
assert.ok(url?.startsWith('http://127.0.0.1:')||url?.startsWith('http://[::1]:'));
const browser=await chromium.launch({executablePath:process.env.PC_CLUB_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox']});
try{
 const remote=await browser.newContext({viewport:{width:390,height:844}});const manager=await remote.newPage();manager.setDefaultTimeout(15000);
 const errors=[];manager.on('pageerror',e=>errors.push(e.message));
 await manager.goto(url);
 await manager.getByRole('button',{name:'Управляющий · пароль',exact:true}).click();
 await manager.getByLabel('Логин управляющего',{exact:true}).fill('manager');
 await manager.getByLabel('Пароль',{exact:true}).fill(process.env.PC_CLUB_E2E_MANAGER_PASSWORD);
 await manager.getByRole('button',{name:'Войти как управляющий',exact:true}).click();
 await manager.getByRole('heading',{level:1,name:'Управляющий настройки'}).waitFor();
 const creating=manager.locator('form').filter({has:manager.getByRole('button',{name:'Создать сотрудника',exact:true})});
 await creating.getByLabel('Имя',{exact:true}).fill('Первый E2E');
 await creating.getByLabel('Личный PIN · 6 цифр',{exact:true}).fill('111111');
 const created=manager.waitForResponse(r=>r.url().endsWith('/users')&&r.request().method()==='POST'&&r.status()===201);
 await creating.getByRole('button').click();const owner=(await (await created).json()).id;
 const employees=manager.getByRole('region',{name:'Управление сотрудниками',exact:true});
 async function edit(){const item=employees.locator('details').filter({hasText:'Изменить: Первый'});if(!await item.evaluate(element=>element.open))await item.locator('summary').click();return item;}
 async function reloadEmployees(){await employees.getByRole('button',{name:'Перечитать сотрудников',exact:true}).click();await manager.waitForFunction(()=>[...document.querySelectorAll('button')].some(button=>button.textContent==='Перечитать сотрудников'&&!button.disabled));}
 let employee=await edit();
 await employee.getByLabel('Имя сотрудника',{exact:true}).fill('Первый администратор E2E');
 await employee.getByRole('button',{name:'Сохранить сотрудника',exact:true}).click();
 await employees.getByRole('status').filter({hasText:'Данные сотрудника сохранены'}).waitFor();
 employee=await edit();await employee.getByLabel('Доступ сотрудника включён',{exact:true}).uncheck();
 await employee.getByRole('button',{name:'Сохранить сотрудника',exact:true}).click();
 await manager.getByText('Первый администратор E2E · администратор · отключён',{exact:true}).waitFor();
 employee=await edit();await employee.getByLabel('Доступ сотрудника включён',{exact:true}).check();
 await employee.getByRole('button',{name:'Сохранить сотрудника',exact:true}).click();
 await manager.getByText('Первый администратор E2E · администратор · активен',{exact:true}).waitFor();
 employee=await edit();await employee.getByLabel('Новый личный PIN',{exact:true}).fill('222222');
 await employee.getByRole('button',{name:'Сбросить PIN',exact:true}).click();
 await employees.getByRole('status').filter({hasText:'PIN изменён'}).waitFor();
 const own=employees.locator('details').filter({hasText:'Изменить: Управляющий настройки'});
 await own.locator('summary').click();await own.getByLabel('Доступ сотрудника включён',{exact:true}).uncheck();
 await own.getByRole('button',{name:'Сохранить сотрудника',exact:true}).click();
 await employees.getByRole('alert').filter({hasText:'последнего управляющего'}).waitFor();
 await reloadEmployees();
 await employees.getByRole('alert').waitFor({state:'hidden'});
 // Enrollment is issued remotely, then used once in a fresh club context.
 await manager.getByLabel('Повторите пароль управляющего',{exact:true}).fill(process.env.PC_CLUB_E2E_MANAGER_PASSWORD);
 await manager.getByRole('button',{name:'Создать код на 10 минут',exact:true}).click();
 await manager.locator('code.code').waitFor();const code=await manager.locator('code.code').textContent();
 const club=await browser.newContext();const page=await club.newPage();page.setDefaultTimeout(15000);page.on('pageerror',e=>errors.push(e.message));
 await page.goto(url);await page.locator('summary').filter({hasText:'Зарегистрировать браузер клуба'}).click();
 await page.getByLabel('Одноразовый код',{exact:true}).fill(code);await page.getByLabel('Название браузера',{exact:true}).fill('Клубный браузер E2E');
 await page.getByRole('button',{name:'Зарегистрировать',exact:true}).click();
 await page.locator('select[name="user"]').waitFor();
 // Manager creates a night shift with explicit zero; admin must accept it personally.
 const opening=manager.locator('form').filter({has:manager.getByRole('button',{name:'Создать opening-черновик',exact:true})});
 await opening.locator('select[name="owner"]').selectOption(owner);
 await opening.getByLabel('Дата начала в часовом поясе клуба',{exact:true}).fill('2026-10-19');
 await opening.locator('select[name="kind"]').selectOption('night');
 await opening.getByLabel('Первоначальный пересчёт · рубли',{exact:true}).fill('0');
 await opening.getByLabel('Основание первоначальной суммы',{exact:true}).fill('Синтетическая проверка первого приёма');
 await opening.getByRole('button').click();
 await manager.getByRole('region',{name:'Текущая смена',exact:true}).getByText('Черновик приёма',{exact:false}).waitFor();
 await page.locator('select[name="user"]').selectOption(owner);
 await page.getByLabel('Личный PIN',{exact:true}).fill('111111');await page.getByRole('button',{name:'Войти по PIN',exact:true}).click();
 await page.getByRole('alert').filter({hasText:'Неверные данные входа'}).waitFor();
 await page.locator('select[name="user"]').selectOption(owner);await page.getByLabel('Личный PIN',{exact:true}).fill('222222');
 await page.getByRole('button',{name:'Войти по PIN',exact:true}).click();
 await page.getByRole('heading',{level:1,name:'Первый администратор E2E'}).waitFor();
 const activate=page.getByRole('button',{name:'Подтвердить первый приём',exact:true});await activate.waitFor();assert.ok(await activate.isDisabled());
 for(let i=0;i<12;i++){
  const item=page.locator('form.checklist-item').nth(i);await item.locator('select[name="answer"]').selectOption('yes');
  const saved=page.waitForResponse(r=>r.url().includes('/checklist/')&&r.request().method()==='PATCH'&&r.status()===200);
  await item.getByRole('button',{name:'Сохранить ответ',exact:true}).click();await saved;
  await item.getByText('Сохранено',{exact:true}).waitFor();
 }
 assert.equal(await page.locator('.cash-panel').count(),1,'checklist saves must not duplicate cash forms');
 await page.getByLabel('Мой пересчёт в рублях',{exact:true}).fill('0');
 await page.getByLabel('Личный PIN для этого действия',{exact:true}).fill('222222');
 await activate.click();
 await page.getByRole('region',{name:'Текущая смена',exact:true}).getByText('Активная смена',{exact:false}).waitFor();
 // A responsible employee cannot be disabled by a manager.
 await reloadEmployees();
 employee=await edit();await employee.getByLabel('Доступ сотрудника включён',{exact:true}).uncheck();
 await employee.getByRole('button',{name:'Сохранить сотрудника',exact:true}).click();
 await employees.getByRole('alert').filter({hasText:'ответственная смена'}).waitFor();
 await manager.locator('summary').filter({hasText:'Журнал действий'}).click();
 const audit=manager.getByRole('region',{name:'Журнал действий',exact:true});
 await audit.getByLabel('Действие (точный код)',{exact:true}).fill('user_updated');await audit.getByRole('button',{name:'Показать журнал',exact:true}).click();
 await audit.locator('ol li summary').first().click();await audit.getByRole('table').waitFor();
 // Roles are assigned explicitly at creation; a manager needs a password,
 // and does not acquire the administrator's PIN authority implicitly.
 await creating.getByLabel('Роль нового сотрудника',{exact:true}).selectOption('manager');
 await creating.getByLabel('Имя',{exact:true}).fill('Второй управляющий E2E');
 await creating.getByLabel('Логин нового управляющего',{exact:true}).fill('manager2');
 await creating.getByLabel('Пароль нового управляющего',{exact:true}).fill('synthetic-second-manager-password');
 const secondCreated=manager.waitForResponse(r=>r.url().endsWith('/users')&&r.request().method()==='POST'&&r.status()===201);
 await creating.getByRole('button',{name:'Создать сотрудника',exact:true}).click();
 assert.deepEqual((await (await secondCreated).json()).roles,['manager']);
 assert.ok(await manager.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),'manager interface fits mobile width');
 assert.equal(errors.length,0,'no page errors');
 await club.close();await remote.close();
}finally{await browser.close();}
