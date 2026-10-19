CREATE TABLE checklist_template_versions (
 version integer PRIMARY KEY CHECK(version>0), published boolean NOT NULL DEFAULT false,
 published_by uuid REFERENCES users(id), published_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE checklist_template_items (
 version integer NOT NULL REFERENCES checklist_template_versions(version),
 code text NOT NULL CHECK(length(code) BETWEEN 1 AND 100),
 phase text NOT NULL CHECK(phase IN ('acceptance','closing')),
 label text NOT NULL CHECK(length(label) BETWEEN 1 AND 500),
 help_text text NOT NULL DEFAULT '',
 allowed_answers text[] NOT NULL CHECK(allowed_answers=CASE WHEN code='conflicts_recorded' THEN ARRAY['yes','no'] ELSE ARRAY['yes'] END),
 required boolean NOT NULL DEFAULT true,
 sort_order integer NOT NULL CHECK(sort_order>0),
 equipment_check boolean NOT NULL DEFAULT false,
 PRIMARY KEY(version,code), UNIQUE(version,sort_order)
);
CREATE TABLE shifts (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 club_id smallint NOT NULL DEFAULT 1 REFERENCES club_settings(id),
 owner_id uuid NOT NULL REFERENCES users(id),
 business_date date NOT NULL,
 kind text NOT NULL CHECK(kind IN ('day','night')),
 status text NOT NULL CHECK(status IN ('opening','active','handover_pending','closed','cancelled')),
 planned_start_at timestamptz NOT NULL, planned_end_at timestamptz NOT NULL,
 actual_start_at timestamptz, actual_end_at timestamptz,
 previous_shift_id uuid UNIQUE REFERENCES shifts(id),
 template_version integer NOT NULL REFERENCES checklist_template_versions(version),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 initial_cash_kopecks bigint CHECK(initial_cash_kopecks>=0),
 bootstrap_reason text NOT NULL DEFAULT '' CHECK(length(bootstrap_reason)<=4000),
 created_by uuid NOT NULL REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(),
 CHECK(planned_end_at>planned_start_at),
 CHECK(actual_end_at IS NULL OR (actual_start_at IS NOT NULL AND actual_end_at>=actual_start_at)),
 CHECK(status NOT IN ('active','handover_pending','closed') OR actual_start_at IS NOT NULL),
 CHECK(status!='closed' OR actual_end_at IS NOT NULL),
 CHECK(status NOT IN ('opening','cancelled') OR (actual_start_at IS NULL AND actual_end_at IS NULL))
);
CREATE UNIQUE INDEX one_responsible_shift ON shifts(club_id) WHERE status IN ('active','handover_pending');
CREATE UNIQUE INDEX one_initial_opening ON shifts(club_id) WHERE status='opening' AND previous_shift_id IS NULL;
CREATE TABLE shift_checklist_entries (
 shift_id uuid NOT NULL REFERENCES shifts(id), code text NOT NULL,
 phase text NOT NULL CHECK(phase IN ('acceptance','closing')),
 label text NOT NULL, help_text text NOT NULL,
 allowed_answers text[] NOT NULL CHECK(allowed_answers=CASE WHEN code='conflicts_recorded' THEN ARRAY['yes','no'] ELSE ARRAY['yes'] END),
 required boolean NOT NULL, sort_order integer NOT NULL,
 equipment_check boolean NOT NULL,
 answer text NOT NULL DEFAULT 'unanswered' CHECK(answer IN ('unanswered','yes','no')),
 comment text NOT NULL DEFAULT '' CHECK(length(comment)<=4000),
 equipment_issue boolean NOT NULL DEFAULT false,
 answered_by uuid REFERENCES users(id), answered_at timestamptz,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 PRIMARY KEY(shift_id,code), UNIQUE(shift_id,sort_order),
 CHECK(NOT equipment_issue OR equipment_check)
);
CREATE TABLE shift_notes (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), shift_id uuid NOT NULL REFERENCES shifts(id),
 checklist_code text, category text NOT NULL CHECK(category IN ('equipment','conflict','general')),
 object_label text NOT NULL DEFAULT '' CHECK(length(object_label)<=200),
 text text NOT NULL CHECK(length(trim(text)) BETWEEN 1 AND 4000),
 created_by uuid NOT NULL REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(),
 updated_by uuid NOT NULL REFERENCES users(id), updated_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 FOREIGN KEY(shift_id,checklist_code) REFERENCES shift_checklist_entries(shift_id,code),
 CHECK(category!='equipment' OR (length(trim(object_label))>0 AND checklist_code IS NOT NULL)),
 CHECK(category!='conflict' OR checklist_code='conflicts_recorded' AND checklist_code IS NOT NULL)
);
CREATE TABLE idempotency_records (
 actor_id uuid NOT NULL REFERENCES users(id), route text NOT NULL,
 key text NOT NULL CHECK(length(key) BETWEEN 16 AND 128), payload_hash text NOT NULL,
 result_code integer NOT NULL, result_body jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL DEFAULT now()+interval '7 days',
 PRIMARY KEY(actor_id,route,key)
);
INSERT INTO checklist_template_versions(version,published) VALUES(1,true);
INSERT INTO checklist_template_items(version,code,phase,label,help_text,allowed_answers,required,sort_order,equipment_check) VALUES
(1,'accept_d','acceptance','Написал(а) в чат, что в пути','Подтверждение действия во внешней системе, без автоматической проверки.',ARRAY['yes'],true,1,false),
(1,'accept_e','acceptance','На месте вовремя, в форме','',ARRAY['yes'],true,2,false),
(1,'accept_f','acceptance','Место/зал в чистоте и порядке','',ARRAY['yes'],true,3,false),
(1,'accept_g','acceptance','Сверены наличные в кассе','',ARRAY['yes'],true,4,false),
(1,'accept_h','acceptance','Проверен холодильник/витрины','',ARRAY['yes'],true,5,false),
(1,'accept_i','acceptance','Проверено неисправное оборудование','Проверено; обнаруженные неисправности записаны. Ремонт до передачи не обязателен.',ARRAY['yes'],true,6,true),
(1,'accept_j','acceptance','Написано в чат «принял смену»','Подтверждение действия во внешней системе, без автоматической проверки.',ARRAY['yes'],true,7,false),
(1,'accept_k','acceptance','Смена открыта в системе (CLS)','Подтверждение действия во внешней системе, без автоматической проверки.',ARRAY['yes'],true,8,false),
(1,'accept_l','acceptance','Товары проверены','',ARRAY['yes'],true,9,false),
(1,'accept_m','acceptance','Эвакуационные выходы проверены','',ARRAY['yes'],true,10,false),
(1,'accept_n','acceptance','Температура в зале настроена (23°C)','',ARRAY['yes'],true,11,false),
(1,'accept_o','acceptance','Проверка Faceit','',ARRAY['yes'],true,12,false),
(1,'close_p','closing','Отчёт по выручке заполнен и сверен','',ARRAY['yes'],true,13,false),
(1,'close_q','closing','Наличные в конверте (имя+дата), в сейфе','',ARRAY['yes'],true,14,false),
(1,'close_r','closing','Чековая лента проверена','',ARRAY['yes'],true,15,false),
(1,'close_s','closing','Холодильник/витрины заполнены','',ARRAY['yes'],true,16,false),
(1,'close_t','closing','Фото витрин отправлено в чат','Подтверждение действия во внешней системе, без автоматической проверки.',ARRAY['yes'],true,17,false),
(1,'close_u','closing','Заканчивающиеся товары переданы','',ARRAY['yes'],true,18,false),
(1,'close_v','closing','ПК и консоли в порядке','Проверено; обнаруженные неисправности записаны. Ремонт до передачи не обязателен.',ARRAY['yes'],true,19,true),
(1,'close_w','closing','Периферия и кресла исправны','Проверено; обнаруженные неисправности записаны. Ремонт до передачи не обязателен.',ARRAY['yes'],true,20,true),
(1,'close_x','closing','Неисправности устранены/зафиксированы','Проверено; обнаруженные неисправности записаны. Ремонт до передачи не обязателен.',ARRAY['yes'],true,21,true),
(1,'close_y','closing','Видео ПК отправлено в чат','Подтверждение действия во внешней системе, без автоматической проверки.',ARRAY['yes'],true,22,false),
(1,'close_z','closing','Периферия протёрта, мусор вынесен','',ARRAY['yes'],true,23,false),
(1,'close_aa','closing','Стол/ресепшн/зал убраны','',ARRAY['yes'],true,24,false),
(1,'close_ab','closing','Рабочее место готово для след. смены','',ARRAY['yes'],true,25,false),
(1,'close_ac','closing','Сессии гостей завершены/переданы','',ARRAY['yes'],true,26,false),
(1,'conflicts_recorded','closing','Конфликтные ситуации зафиксированы','Да — конфликт был, информация записана. Нет — конфликтных ситуаций не было. Пустой ответ недопустим.',ARRAY['yes','no'],true,27,false),
(1,'close_ae','closing','Отчёт и фото отправлены в чат','Подтверждение действия во внешней системе, без автоматической проверки.',ARRAY['yes'],true,28,false),
(1,'close_af','closing','Сообщение о передаче смены отправлено','Подтверждение действия во внешней системе, без автоматической проверки.',ARRAY['yes'],true,29,false),
(1,'close_ag','closing','Следующий админ проинформирован','',ARRAY['yes'],true,30,false),
(1,'close_ah','closing','На телевизорах и PS отсутствует пыль','',ARRAY['yes'],true,31,false);
