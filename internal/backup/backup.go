// Package backup — бэкапы виртуальных машин libvirt, контейнеров
// Docker/Podman и compose-стеков на самом хосте.
//
// Бэкап — один tar-файл в <DataDir>/backups/<вид>/<имя>/: внутри
// manifest.json и то, из чего объект собирается обратно. Сценарии —
// bash, выполняются вне песочницы юнита (virsh, qemu-img, docker — там)
// фоновым заданием с живым журналом.
package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/msgs"
)

// Виды бэкапа.
const (
	KindVM         = "vm"
	KindDocker     = "docker"
	KindPodman     = "podman"
	KindCompose    = "compose"
	JobKindBackup  = "backup.create"
	JobKindRestore = "backup.restore"
)

// Kinds — допустимые виды.
var Kinds = []string{KindVM, KindDocker, KindPodman, KindCompose}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// ValidName — имя машины, контейнера или проекта compose: ничего, что
// могло бы выйти из каталога или попасть в командную строку.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// ValidKind — вид из Kinds.
func ValidKind(kind string) bool {
	for _, k := range Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Manifest — что лежит в бэкапе; пишется сценарием в manifest.json.
type Manifest struct {
	Kind    string    `json:"kind"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	// Для машины — файлы дисков в архиве по target (vda.qcow2 …);
	// для контейнера — образ и тома; для compose — каталог проекта.
	Files []string `json:"files,omitempty"`
	Note  string   `json:"note,omitempty"`
}

// Entry — бэкап в списке.
type Entry struct {
	Kind    string    `json:"kind"`
	Name    string    `json:"name"`
	File    string    `json:"file"` // имя файла в каталоге объекта
	Path    string    `json:"path"` // полный путь на хосте
	Size    int64     `json:"size"`
	Created time.Time `json:"created"`
}

// Dir — каталог бэкапов объекта.
func Dir(root, kind, name string) string { return filepath.Join(root, kind, name) }

// List — бэкапы объекта (name пусто — все объекты вида), новые сверху.
func List(root, kind, name string) ([]Entry, error) {
	if !ValidKind(kind) || (name != "" && !ValidName(name)) {
		return nil, msgs.Errorf("backup.badTarget", kind, name)
	}
	var dirs []string
	if name != "" {
		dirs = []string{Dir(root, kind, name)}
	} else {
		ents, err := os.ReadDir(filepath.Join(root, kind))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, e := range ents {
			if e.IsDir() && ValidName(e.Name()) {
				dirs = append(dirs, filepath.Join(root, kind, e.Name()))
			}
		}
	}
	var out []Entry
	for _, d := range dirs {
		ents, err := os.ReadDir(d)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, Entry{
				Kind: kind, Name: filepath.Base(d), File: e.Name(), Path: filepath.Join(d, e.Name()),
				Size: info.Size(), Created: info.ModTime(),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// Resolve проверяет, что путь — бэкап внутри root (для скачивания,
// удаления и восстановления), и возвращает его чистым.
func Resolve(root, path string) (string, error) {
	clean := filepath.Clean(path)
	rel, err := filepath.Rel(root, clean)
	if err != nil || strings.HasPrefix(rel, "..") || !strings.HasSuffix(clean, ".tar") {
		return "", msgs.Errorf("backup.badPath", path)
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 3 || !ValidKind(parts[0]) || !ValidName(parts[1]) {
		return "", msgs.Errorf("backup.badPath", path)
	}
	return clean, nil
}

// stamp — метка времени в имени файла.
func stamp(t time.Time) string { return t.UTC().Format("20060102-150405") }

// Params — параметры задания бэкапа.
type Params struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Compose: каталог проекта (где compose-файл) и нужны ли образы.
	ProjectDir    string `json:"project_dir,omitempty"`
	IncludeImages bool   `json:"include_images,omitempty"`
}

// RestoreParams — параметры восстановления.
type RestoreParams struct {
	Path string `json:"path"`
	// NewName — имя восстановленного объекта; пусто — исходное (поверх).
	NewName string `json:"new_name,omitempty"`
}

// sh — одинарные кавычки для bash.
func sh(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Script — сценарий бэкапа: пишет <dir>/<name>-<stamp>.tar.
func Script(root string, p Params, now time.Time) (string, string, error) {
	if !ValidKind(p.Kind) || !ValidName(p.Name) {
		return "", "", msgs.Errorf("backup.badTarget", p.Kind, p.Name)
	}
	dir := Dir(root, p.Kind, p.Name)
	base := p.Name + "-" + stamp(now)
	out := filepath.Join(dir, base+".tar")
	head := fmt.Sprintf(`set -euo pipefail
DIR=%s
WORK="$DIR/%s.partial"
OUT=%s
NAME=%s
mkdir -p "$DIR"
rm -rf "$WORK"; mkdir -p "$WORK"
trap 'rm -rf "$WORK"' EXIT
manifest() { printf '{"kind":"%%s","name":"%%s","created":"%%s","files":[%%s]}\n' "$1" "$NAME" "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)" "$2" > "$WORK/manifest.json"; }
`, sh(dir), base, sh(out), sh(p.Name))
	var body string
	switch p.Kind {
	case KindVM:
		body = vmBackupScript
	case KindDocker:
		body = strings.ReplaceAll(containerBackupScript, "ENGINE", "docker")
	case KindPodman:
		body = strings.ReplaceAll(containerBackupScript, "ENGINE", "podman")
	case KindCompose:
		if p.ProjectDir == "" || !filepath.IsAbs(p.ProjectDir) {
			return "", "", msgs.Errorf("backup.composeNeedsDir")
		}
		images := "0"
		if p.IncludeImages {
			images = "1"
		}
		body = fmt.Sprintf("PROJECT_DIR=%s\nWITH_IMAGES=%s\n", sh(p.ProjectDir), images) + composeBackupScript
	}
	tail := `
echo "--- tar: $OUT"
tar -C "$WORK" -cf "$OUT.tmp" .
mv "$OUT.tmp" "$OUT"
ls -l "$OUT"
echo "--- done"
`
	return head + body + tail, out, nil
}

// Бэкап машины: XML и диски. Работающая машина не останавливается:
// диски на время копирования переводятся на внешний снимок (overlay),
// базовые образы копируются, затем изменения из overlay возвращаются
// обратно (blockcommit --pivot). С qemu-guest-agent снимок согласован
// (--quiesce), без него — как после внезапного выключения.
const vmBackupScript = `
command -v virsh >/dev/null || { echo "virsh is missing"; exit 1; }
command -v qemu-img >/dev/null || { echo "qemu-img is missing"; exit 1; }
virsh dumpxml "$NAME" > "$WORK/domain.xml"
state=$(virsh domstate "$NAME")
echo "state: $state"
mapfile -t DISKS < <(virsh domblklist "$NAME" --details | awk '$1=="file" && $2=="disk" {print $3" "$4}')
[ ${#DISKS[@]} -gt 0 ] || echo "no file-backed disks"
files=""
if [ "$state" = "running" ] || [ "$state" = "paused" ]; then
  SNAP="nkt-backup-$(date +%s)"
  specs=()
  for d in "${DISKS[@]}"; do t=${d%% *}; specs+=(--diskspec "$t,snapshot=external,file=/var/lib/libvirt/images/$NAME-$t.$SNAP.overlay"); done
  echo "--- external disk-only snapshot $SNAP"
  virsh snapshot-create-as "$NAME" "$SNAP" --disk-only --atomic --no-metadata --quiesce "${specs[@]}" 2>/dev/null \
    || virsh snapshot-create-as "$NAME" "$SNAP" --disk-only --atomic --no-metadata "${specs[@]}"
  # Страховка: упади сценарий после снимка — машина осталась бы на
  # overlay-файлах. При выходе с ошибкой изменения возвращаются в диски.
  PENDING=()
  for d in "${DISKS[@]}"; do PENDING+=("${d%% *}"); done
  commit_pending() {
    for t in "${PENDING[@]}"; do
      [ -n "$t" ] || continue
      echo "--- recovery: blockcommit $t"
      virsh blockcommit "$NAME" "$t" --active --pivot --wait || echo "!!! blockcommit $t failed: the machine still runs on /var/lib/libvirt/images/$NAME-$t.$SNAP.overlay"
    done
  }
  trap 'rc=$?; [ $rc -ne 0 ] && commit_pending; rm -rf "$WORK"' EXIT
  for d in "${DISKS[@]}"; do
    t=${d%% *}; src=${d#* }
    echo "--- copy $t: $src"
    qemu-img convert -p -O qcow2 -c "$src" "$WORK/$t.qcow2"
    files="$files\"$t.qcow2\","
    echo "--- blockcommit $t"
    virsh blockcommit "$NAME" "$t" --active --pivot --wait --verbose
    PENDING=("${PENDING[@]/$t}")
    rm -f "/var/lib/libvirt/images/$NAME-$t.$SNAP.overlay"
  done
else
  for d in "${DISKS[@]}"; do
    t=${d%% *}; src=${d#* }
    echo "--- copy $t: $src"
    qemu-img convert -p -O qcow2 -c "$src" "$WORK/$t.qcow2"
    files="$files\"$t.qcow2\","
  done
fi
manifest vm "${files%,}"
`

// Бэкап контейнера: образ из текущего состояния (commit → save),
// конфигурация (inspect) и именованные тома — tar их каталогов прямо с
// хоста, без вспомогательного контейнера.
const containerBackupScript = `
command -v ENGINE >/dev/null || { echo "ENGINE is missing"; exit 1; }
ENGINE inspect "$NAME" > "$WORK/inspect.json"
IMG="nkt-backup/$NAME:$(date +%Y%m%d%H%M%S)"
IMG=$(echo "$IMG" | tr 'A-Z' 'a-z')
echo "--- commit $NAME -> $IMG"
ENGINE commit "$NAME" "$IMG" >/dev/null
echo "--- save image"
ENGINE save -o "$WORK/image.tar" "$IMG"
ENGINE rmi "$IMG" >/dev/null || true
echo "$IMG" > "$WORK/image.ref"
files="\"image.tar\","
mkdir -p "$WORK/volumes"
for vol in $(ENGINE inspect -f '{{range .Mounts}}{{if eq .Type "volume"}}{{.Name}} {{end}}{{end}}' "$NAME"); do
  mp=$(ENGINE volume inspect -f '{{.Mountpoint}}' "$vol")
  echo "--- volume $vol ($mp)"
  tar -C "$mp" -czf "$WORK/volumes/$vol.tgz" .
  files="$files\"volumes/$vol.tgz\","
done
manifest ENGINE "${files%,}"
`

// Бэкап compose-стека: каталог проекта (compose-файл, .env, конфиги),
// именованные тома проекта и, по галочке, образы.
const composeBackupScript = `
command -v docker >/dev/null || { echo "docker is missing"; exit 1; }
[ -d "$PROJECT_DIR" ] || { echo "no such directory: $PROJECT_DIR"; exit 1; }
echo "--- project dir $PROJECT_DIR"
tar -C "$(dirname "$PROJECT_DIR")" -czf "$WORK/project.tgz" "$(basename "$PROJECT_DIR")"
echo "$PROJECT_DIR" > "$WORK/project.path"
files="\"project.tgz\","
mkdir -p "$WORK/volumes"
for vol in $(docker volume ls -q --filter "label=com.docker.compose.project=$NAME"); do
  mp=$(docker volume inspect -f '{{.Mountpoint}}' "$vol")
  echo "--- volume $vol ($mp)"
  tar -C "$mp" -czf "$WORK/volumes/$vol.tgz" .
  files="$files\"volumes/$vol.tgz\","
done
if [ "$WITH_IMAGES" = 1 ]; then
  imgs=$(docker ps -a --filter "label=com.docker.compose.project=$NAME" --format '{{.Image}}' | sort -u)
  if [ -n "$imgs" ]; then
    echo "--- save images: $imgs"
    docker save -o "$WORK/images.tar" $imgs
    files="$files\"images.tar\","
  fi
fi
manifest compose "${files%,}"
`

// RestoreScript — сценарий восстановления из архива.
func RestoreScript(root string, p RestoreParams) (string, error) {
	path, err := Resolve(root, p.Path)
	if err != nil {
		return "", err
	}
	rel, _ := filepath.Rel(root, path)
	parts := strings.Split(rel, string(filepath.Separator))
	kind, orig := parts[0], parts[1]
	name := orig
	if p.NewName != "" {
		if !ValidName(p.NewName) {
			return "", msgs.Errorf("backup.badTarget", kind, p.NewName)
		}
		name = p.NewName
	}
	head := fmt.Sprintf(`set -euo pipefail
ARCHIVE=%s
ORIG=%s
NAME=%s
WORK=$(mktemp -d /var/tmp/nkt-restore.XXXXXX)
trap 'rm -rf "$WORK"' EXIT
echo "--- unpack $ARCHIVE"
tar -C "$WORK" -xf "$ARCHIVE"
cat "$WORK/manifest.json"; echo
`, sh(path), sh(orig), sh(name))
	switch kind {
	case KindVM:
		return head + vmRestoreScript, nil
	case KindDocker:
		return head + strings.ReplaceAll(containerRestoreScript, "ENGINE", "docker"), nil
	case KindPodman:
		return head + strings.ReplaceAll(containerRestoreScript, "ENGINE", "podman"), nil
	case KindCompose:
		return head + composeRestoreScript, nil
	}
	return "", msgs.Errorf("backup.badPath", p.Path)
}

// Восстановление машины: под новым именем — копия (новые UUID и MAC,
// диски <имя>-<target>.qcow2); под прежним — только если она выключена,
// диски заменяются на месте.
const vmRestoreScript = `
command -v python3 >/dev/null || { echo "python3 is required to rewrite the domain XML"; exit 1; }
IMAGES=/var/lib/libvirt/images
XML="$WORK/domain.xml"
if [ "$NAME" != "$ORIG" ]; then
  echo "--- restore as a new machine $NAME"
  sed -i -e "s#<name>$ORIG</name>#<name>$NAME</name>#" -e '/<uuid>/d' -e '/<mac address=/d' "$XML"
else
  if virsh dominfo "$NAME" >/dev/null 2>&1; then
    st=$(virsh domstate "$NAME")
    [ "$st" = "shut off" ] || { echo "machine $NAME is $st: shut it down before restoring over it"; exit 1; }
  fi
fi
for f in "$WORK"/*.qcow2; do
  [ -e "$f" ] || continue
  t=$(basename "$f" .qcow2)
  dst="$IMAGES/$NAME-$t.qcow2"
  old=$(virsh domblklist "$NAME" --details 2>/dev/null | awk -v t="$t" '$3==t {print $4}' || true)
  [ "$NAME" = "$ORIG" ] && [ -n "$old" ] && dst="$old"
  echo "--- disk $t -> $dst"
  cp --sparse=always "$f" "$dst"
  # путь диска в XML — на восстановленный файл
  python3 - "$XML" "$t" "$dst" <<'PY'
import re, sys
xml, target, dst = sys.argv[1], sys.argv[2], sys.argv[3]
s = open(xml).read()
def fix(m):
    block = m.group(0)
    if re.search(r"<target dev='%s'" % re.escape(target), block):
        block = re.sub(r"<source file='[^']*'", "<source file='%s'" % dst, block)
    return block
s = re.sub(r"<disk[^>]*>.*?</disk>", fix, s, flags=re.S)
open(xml, 'w').write(s)
PY
done
echo "--- virsh define"
virsh define "$XML"
echo "--- done"
`

// Восстановление контейнера: образ загружается, тома восстанавливаются,
// контейнер создаётся заново по основным параметрам из inspect (порты,
// окружение, тома, сеть, политика перезапуска) — остальное из образа.
const containerRestoreScript = `
command -v ENGINE >/dev/null || { echo "ENGINE is missing"; exit 1; }
command -v python3 >/dev/null || { echo "python3 is required to rebuild the run options"; exit 1; }
echo "--- load image"
ENGINE load -i "$WORK/image.tar"
IMG=$(cat "$WORK/image.ref")
for t in "$WORK"/volumes/*.tgz; do
  [ -e "$t" ] || continue
  vol=$(basename "$t" .tgz)
  [ "$NAME" != "$ORIG" ] && newvol="$NAME-$vol" || newvol="$vol"
  ENGINE volume create "$newvol" >/dev/null
  mp=$(ENGINE volume inspect -f '{{.Mountpoint}}' "$newvol")
  echo "--- volume $newvol"
  tar -C "$mp" -xzf "$t"
done
if ENGINE inspect "$NAME" >/dev/null 2>&1; then
  [ "$NAME" = "$ORIG" ] || { echo "container $NAME already exists"; exit 1; }
  echo "--- remove existing $NAME"
  ENGINE rm -f "$NAME" >/dev/null
fi
args=$(python3 - "$WORK/inspect.json" "$NAME" "$ORIG" <<'PY'
import json, shlex, sys
c = json.load(open(sys.argv[1]))[0]
name, orig = sys.argv[2], sys.argv[3]
hc, cfg = c.get("HostConfig", {}), c.get("Config", {})
a = ["--name", name]
pol = (hc.get("RestartPolicy") or {}).get("Name")
if pol and pol != "no": a += ["--restart", pol]
for cport, binds in (hc.get("PortBindings") or {}).items():
    for b in binds or []:
        hp = b.get("HostPort")
        if hp and name == orig:
            a += ["-p", (b.get("HostIp") + ":" if b.get("HostIp") else "") + hp + ":" + cport]
for e in cfg.get("Env") or []: a += ["-e", e]
for m in c.get("Mounts") or []:
    if m.get("Type") == "volume":
        v = m["Name"] if name == orig else name + "-" + m["Name"]
        a += ["-v", v + ":" + m["Destination"]]
    elif m.get("Type") == "bind":
        a += ["-v", m["Source"] + ":" + m["Destination"]]
net = hc.get("NetworkMode")
if net and net not in ("default", "bridge"): a += ["--network", net]
print(" ".join(shlex.quote(x) for x in a))
PY
)
echo "--- create: ENGINE run -d $args $IMG"
eval ENGINE run -d $args "$IMG"
echo "--- done"
`

// Восстановление compose-стека: каталог проекта на прежнее место (под
// новым именем — рядом, <каталог>-<имя>), тома, образы, compose up.
const composeRestoreScript = `
command -v docker >/dev/null || { echo "docker is missing"; exit 1; }
PROJECT_DIR=$(cat "$WORK/project.path")
PARENT=$(dirname "$PROJECT_DIR")
if [ "$NAME" != "$ORIG" ]; then
  TARGET="$PARENT/$(basename "$PROJECT_DIR")-$NAME"
else
  TARGET="$PROJECT_DIR"
  echo "--- compose down (existing stack)"
  (cd "$PROJECT_DIR" 2>/dev/null && docker compose -p "$ORIG" down) || true
fi
mkdir -p "$WORK/p"
tar -C "$WORK/p" -xzf "$WORK/project.tgz"
rm -rf "$TARGET"
mv "$WORK/p/$(basename "$PROJECT_DIR")" "$TARGET"
echo "--- project restored to $TARGET"
if [ -f "$WORK/images.tar" ]; then echo "--- load images"; docker load -i "$WORK/images.tar"; fi
for t in "$WORK"/volumes/*.tgz; do
  [ -e "$t" ] || continue
  vol=$(basename "$t" .tgz)
  newvol=$vol
  [ "$NAME" != "$ORIG" ] && newvol="${NAME}_${vol#${ORIG}_}"
  docker volume create --label "com.docker.compose.project=$NAME" "$newvol" >/dev/null
  mp=$(docker volume inspect -f '{{.Mountpoint}}' "$newvol")
  echo "--- volume $newvol"
  tar -C "$mp" -xzf "$t"
done
echo "--- compose up"
cd "$TARGET" && docker compose -p "$NAME" up -d
echo "--- done"
`

// ReadManifest — manifest.json из архива (для списка и подтверждения).
func ReadManifest(raw []byte) (Manifest, error) {
	var m Manifest
	err := json.Unmarshal(raw, &m)
	return m, err
}
