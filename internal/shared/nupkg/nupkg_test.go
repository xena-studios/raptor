package nupkg

import "testing"

func TestPluginDLL(t *testing.T) {
	for _, tc := range []struct {
		names []string
		want  string
		ok    bool
	}{
		{[]string{"Kr4ken.NewEssentials.nuspec", "lib/net461/NewEssentials.dll", ".signature.p7s"}, "lib/net461/NewEssentials.dll", true},
		// The best framework wins.
		{[]string{"lib/net48/A.dll", "lib/netstandard2.0/A.dll", "lib/netstandard2.1/A.dll"}, "lib/netstandard2.1/A.dll", true},
		{[]string{"lib/netstandard2.1/UnturnedImages.dll", "lib/netstandard2.1/UnturnedImages.xml"}, "lib/netstandard2.1/UnturnedImages.dll", true},
		// Two assemblies: not one Raptor installs.
		{[]string{"lib/net461/A.dll", "lib/net461/B.dll"}, "lib/net461/A.dll", false},
		{[]string{"lib/net6.0/A.dll"}, "", false},
		{[]string{"lib/net461/../../A.dll", `lib\net461\A.dll`, "lib/net461/sub/A.dll", "content/A.dll"}, "", false},
		{nil, "", false},
	} {
		got, ok := PluginDLL(tc.names)
		if got != tc.want || ok != tc.ok {
			t.Errorf("PluginDLL(%q) = %q, %v; want %q, %v", tc.names, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFileName(t *testing.T) {
	name := FileName("Kr4ken.NewEssentials", "1.5.1")
	if name != "Kr4ken.NewEssentials@1.5.1.dll" {
		t.Fatal(name)
	}
	for in, want := range map[string]bool{
		name: true, name + ".disabled": true, "A@1.0.0-beta.2.dll": true,
		"NewEssentials.dll": false, "A@latest.dll": false, "../A@1.0.0.dll": false, "A@1.0.0.zip": false,
	} {
		if _, _, ok := ParseFileName(in); ok != want {
			t.Errorf("ParseFileName(%q): %v", in, ok)
		}
	}
	if p, v, _ := ParseFileName(name); p != "Kr4ken.NewEssentials" || v != "1.5.1" {
		t.Errorf("%s %s", p, v)
	}
}
