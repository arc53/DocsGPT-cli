package host

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderWindowsTaskXMLBasics(t *testing.T) {
	got := RenderWindowsTaskXML(`C:\Users\alex\bin\docsgpt-cli.exe`, `DESKTOP-1\alex`)

	for _, want := range []string{
		`<Command>C:\Users\alex\bin\docsgpt-cli.exe</Command>`,
		"<Arguments>host --service</Arguments>",
		`<UserId>DESKTOP-1\alex</UserId>`,
		"<LogonTrigger>",
		"<LogonType>InteractiveToken</LogonType>",
		"<RunLevel>LeastPrivilege</RunLevel>",
		// The default PT72H limit would kill the daemon after three days.
		"<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>",
		"<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>",
		"<RestartOnFailure>",
		"<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>",
		"<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("task XML missing %q\n%s", want, got)
		}
	}
}

func TestRenderWindowsTaskXMLEscapes(t *testing.T) {
	got := RenderWindowsTaskXML(`C:\Tools & Bin\docsgpt-cli.exe`, `D<M>N\alex`)
	if !strings.Contains(got, `C:\Tools &amp; Bin\docsgpt-cli.exe`) {
		t.Errorf("exe path not XML-escaped:\n%s", got)
	}
	if !strings.Contains(got, `D&lt;M&gt;N\alex`) {
		t.Errorf("user not XML-escaped:\n%s", got)
	}
	if strings.Contains(got, `Tools & Bin`) {
		t.Errorf("raw ampersand leaked into XML:\n%s", got)
	}
}

// TestRenderWindowsTaskXMLWellFormed walks the document with an XML decoder
// so a malformed template (unclosed tag, bad escaping) fails the build, not
// the user's schtasks run.
func TestRenderWindowsTaskXMLWellFormed(t *testing.T) {
	got := RenderWindowsTaskXML(`C:\Program Files (x86)\docsgpt & co\cli.exe`, `DOMAIN\user`)
	dec := xml.NewDecoder(strings.NewReader(got))
	for {
		_, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("task XML is not well-formed: %v\n%s", err, got)
		}
	}
}

func TestWindowsTaskXMLPathUnderDocsgptDir(t *testing.T) {
	p := WindowsTaskXMLPath()
	if !strings.Contains(p, ".docsgpt") {
		t.Errorf("task XML path %q not under ~/.docsgpt", p)
	}
	if filepath.Base(p) != WindowsTaskName+".task.xml" {
		t.Errorf("task XML basename = %q", filepath.Base(p))
	}
}

func TestWriteWindowsTaskXMLCreatesParents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "task.xml")
	if err := WriteWindowsTaskXML(path, "<Task/>"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "<Task/>" {
		t.Errorf("content = %q", data)
	}
}

func TestWindowsTaskRegisterCmd(t *testing.T) {
	got := WindowsTaskRegisterCmd(`C:\Users\alex\.docsgpt\docsgpt-cli-host.task.xml`)
	want := `schtasks /Create /TN docsgpt-cli-host /XML "C:\Users\alex\.docsgpt\docsgpt-cli-host.task.xml" /F`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
