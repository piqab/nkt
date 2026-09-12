package control

import "testing"

func TestBrowseDirListsHomeDirectory(t *testing.T) {
	m := configsSetup(t)
	entries, err := m.BrowseDir("/home")
	if err != nil {
		t.Fatalf("BrowseDir: %v", err)
	}
	var sawAlice, sawBob bool
	for _, e := range entries {
		switch e.Path {
		case "/home/alice":
			sawAlice = e.IsDir
		case "/home/bob":
			sawBob = e.IsDir
		}
	}
	if !sawAlice || !sawBob {
		t.Errorf("entries = %+v, ожидались каталоги alice и bob", entries)
	}
}

func TestBrowseDirDefaultsToHome(t *testing.T) {
	m := configsSetup(t)
	withPath, err := m.BrowseDir("/home")
	if err != nil {
		t.Fatalf("BrowseDir(/home): %v", err)
	}
	withEmpty, err := m.BrowseDir("")
	if err != nil {
		t.Fatalf("BrowseDir(\"\"): %v", err)
	}
	if len(withPath) != len(withEmpty) {
		t.Errorf("пустой путь должен вести себя как /home: %d vs %d записей", len(withEmpty), len(withPath))
	}
}

func TestBrowseDirListsUserDirectory(t *testing.T) {
	m := configsSetup(t)
	entries, err := m.BrowseDir("/home/alice")
	if err != nil {
		t.Fatalf("BrowseDir: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Path == "/home/alice/docker-compose.yml" {
			found = true
		}
	}
	if !found {
		t.Errorf("entries = %+v, ожидался docker-compose.yml", entries)
	}
}

// Ходить можно только по корням категорий: /home и каталог стеков у
// docker, /etc/nginx у nginx и так далее. /etc целиком, корень и похожие
// на корень имена — нет.
func TestBrowseDirRejectsOutsideRoots(t *testing.T) {
	m := configsSetup(t)
	for _, path := range []string{"/etc", "/", "/homeless", "/etc/nginxx", "/etc/passwd"} {
		if _, err := m.BrowseDir(path); err == nil {
			t.Errorf("BrowseDir(%q): ожидалась ошибка вне корней категорий", path)
		}
	}
	// А корень категории — можно: сюда и ведёт «новый файл».
	if _, err := m.BrowseDir("/etc/nginx"); err != nil {
		t.Errorf("BrowseDir(/etc/nginx): %v, а это корень категории nginx", err)
	}
	roots := m.CategoryRoots()
	if len(roots["docker"]) != 2 || roots["nginx"][0] != "/etc/nginx" {
		t.Errorf("корни категорий = %v", roots)
	}
}

func TestBrowseDirRejectsTraversal(t *testing.T) {
	m := configsSetup(t)
	if _, err := m.BrowseDir("/home/../etc"); err == nil {
		t.Error("ожидалась ошибка для пути с ..")
	}
}

// A compose-named file under /home must be writable even though it is
// neither in NKT_COMPOSE_FILES nor known from any prior scan — the whole
// point is reaching a stack the operator is only now creating.
func TestServiceForPathTrustsComposeFileUnderHome(t *testing.T) {
	m := configsSetup(t)
	if _, err := m.Read("/home/alice/docker-compose.yml"); err != nil {
		t.Errorf("Read: %v", err)
	}
}

func TestServiceForPathRejectsNonComposeFileUnderHome(t *testing.T) {
	m := configsSetup(t)
	if _, err := m.Read("/home/bob/notes.txt"); err == nil {
		t.Error("ожидалась ошибка для не-compose файла под /home")
	}
}

func TestMkdirCreatesDirectoryUnderHome(t *testing.T) {
	m := configsSetup(t)
	if err := m.Mkdir("/home/dave/apps"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	entries, err := m.BrowseDir("/home")
	if err != nil {
		t.Fatalf("BrowseDir: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Path == "/home/dave" && e.IsDir {
			found = true
		}
	}
	if !found {
		t.Errorf("entries = %+v, ожидался новый каталог /home/dave", entries)
	}

	// The nested directory must exist too, ready for a compose file straight away.
	nested, err := m.BrowseDir("/home/dave")
	if err != nil {
		t.Fatalf("BrowseDir(/home/dave): %v", err)
	}
	if len(nested) != 1 || nested[0].Path != "/home/dave/apps" || !nested[0].IsDir {
		t.Errorf("nested = %+v, ожидался только каталог /home/dave/apps", nested)
	}
}

func TestMkdirRejectsOutsideHome(t *testing.T) {
	m := configsSetup(t)
	for _, path := range []string{"", "/etc/newdir", "/home/../etc/newdir", "/"} {
		if err := m.Mkdir(path); err == nil {
			t.Errorf("Mkdir(%q): ожидалась ошибка вне /home", path)
		}
	}
}
