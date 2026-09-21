package bootstrap

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// 原全局初始化断言迁至实际拥有者，每项测试恢复进程原时区。
func preserveTimezone(t *testing.T) {
	t.Helper()
	previous := time.Local
	t.Cleanup(func() { time.Local = previous })
}

func TestInit(t *testing.T) {
	preserveTimezone(t)
	if err := InitTimezone("Asia/Shanghai"); err != nil {
		t.Fatal(err)
	}
	if time.Local.String() != "Asia/Shanghai" {
		t.Errorf("time.Local not set correctly: %s", time.Local)
	}
	calendar := timezone.NewCalendar(time.Local)
	if calendar.Location().String() != "Asia/Shanghai" {
		t.Errorf("calendar did not capture bootstrap location: %s", calendar.Location())
	}
}

func TestInitInvalidTimezone(t *testing.T) {
	preserveTimezone(t)
	previous := time.Local
	if err := InitTimezone("Invalid/Timezone"); err == nil {
		t.Fatal("invalid timezone must fail")
	}
	if time.Local != previous {
		t.Fatal("failed initialization changed timezone")
	}
}

func TestInitDefaultTimezone(t *testing.T) {
	preserveTimezone(t)
	if err := InitTimezone(""); err != nil {
		t.Fatal(err)
	}
	if time.Local.String() != "Asia/Shanghai" {
		t.Fatalf("default timezone changed: %s", time.Local)
	}
}

func TestTimeNowAffected(t *testing.T) {
	preserveTimezone(t)
	if err := InitTimezone("UTC"); err != nil {
		t.Fatal(err)
	}
	utcNow := time.Now()
	if err := InitTimezone("Asia/Shanghai"); err != nil {
		t.Fatal(err)
	}
	shanghaiNow := time.Now()
	_, utcOffset := utcNow.Zone()
	_, shanghaiOffset := shanghaiNow.Zone()
	if difference := shanghaiOffset - utcOffset; difference != 8*3600 {
		t.Errorf("timezone offset difference: got %d, want %d", difference, 8*3600)
	}
}
