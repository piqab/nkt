# httpbin — пример выкладки compose-стека через хаб nkt

[httpbin](https://httpbin.org) — сервис для проверки HTTP-клиентов
(`/get`, `/headers`, `/status/418`, `/delay/2`…). Пример показывает
`action: compose` и вкладку «Сайты» на готовом образе.

- `deploy/docker-compose.yml` — стек: один сервис `httpbin`, порт 8080
  не публикуется;
- `deploy/pipeline.yaml` — конвейер.

## Почему не compose из postmanlabs/httpbin

Compose-файл оригинала,
[postmanlabs/httpbin](https://github.com/postmanlabs/httpbin/blob/master/docker-compose.yml),
собирает образ из исходников (`build: '.'`) и занимает порт 80. Выкладка nkt
берёт только готовые образы — хаб остановит её до обращения к хостам:

```
сервис httpbin собирается из исходников (build:) — укажите готовый образ (image:)
```

а порт 80 нужен прокси. Здесь — [go-httpbin](https://github.com/mccutchen/go-httpbin),
совместимый по API клон на Go: готовый образ `ghcr.io/mccutchen/go-httpbin`
под amd64 и arm64. В ghcr.io версионные теги кончаются на v2.16.1, свежие
версии — только `latest` (выкладка каждый раз делает `pull`); зафиксировать
версию — образ того же автора с Docker Hub, `mccutchen/go-httpbin:2.25.0`.

## 1. Конвейер

«Выкладки» → «Новый конвейер» → «Compose по ссылке»:

```
https://github.com/piqab/nkt/blob/main/examples/httpbin/deploy/docker-compose.yml
```

выберите хост, имя стека — `httpbin` (иначе стек назовётся по
репозиторию, `nkt`) — «Заполнить описание» (или вставьте `deploy/pipeline.yaml`,
поправив `hosts`). «Сохранить», затем «Выложить». В журнале задания:
файлы записаны в `/srv/compose/httpbin`, `pull`, `up -d --wait` — ждёт, пока
контейнер запустится (своего healthcheck у образа нет: он собран на
distroless, внутри нет ни shell, ни curl).

## 2. Сайт

«Выкладки» → «Сайты» → «Новый сайт»:

1. хост — тот же, имя — `httpbin.example.com` (A-запись уже указывает на
   хост) → «Проверить»;
2. прокси — какой есть на хосте (нет ни одного — поставится nginx);
3. цель — стек `httpbin`, сервис `httpbin`, порт `8080`: nkt опубликует его
   на `127.0.0.1` файлом `compose.nkt.yml` рядом со стеком;
4. «Настроить» — сертификат, конфигурация прокси, проверка HTTPS.

Проверка:

```sh
curl https://httpbin.example.com/get
curl -i https://httpbin.example.com/status/418
```

Раскомментируйте `site:` в конвейере — после каждой выкладки хаб проверит
сайт по HTTPS.

## Без прокси

Чтобы проверить стек на хосте без сайта, раскомментируйте в
`docker-compose.yml` строку `ports: ["127.0.0.1:8080:8080"]` и на хосте:
`curl http://127.0.0.1:8080/get`.
