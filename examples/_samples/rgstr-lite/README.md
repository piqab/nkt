# rgstr-lite — учебный плагин

Плагин, который по шагам собирает статья справки «Пишем плагин по шагам»:
сводка с находками, репозитории и теги, удалить тег, собрать мусор, робот
для конвейера (секрет — один раз окну или в конвейер: `.env` или ключ
registry). Полный плагин для работы — **rgstr** в каталоге хаба.

Загрузка: «Плагины» → «Каталог» → «Загрузить свой плагин» → архив
`rgstr-lite.tar.gz` (кнопка «Скачать пример» или
`tar czf rgstr-lite.tar.gz -C examples/_samples rgstr-lite`).

Настройки: `RGSTR_URL`, `RGSTR_USER`, `RGSTR_PASSWORD` (секрет),
`RGSTR_PUBLIC` — как у плагина rgstr. Команда сценария:

```
rgstr-lite gc
rgstr-lite robot ci project shop to-pipeline shop env @registry
```
