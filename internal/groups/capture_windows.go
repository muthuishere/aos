package groups

import (
	"fmt"

	"github.com/muthuishere/aos/internal/sys"
)

func takeScreenshot(req captureRequest) error {
	if req.Mode != captureFullscreen {
		// Snip & Sketch owns interactive capture on Windows and always hands
		// the result to the clipboard, so honour --copy and reject a --out that
		// it cannot satisfy.
		if !req.Copy {
			return fmt.Errorf("%s capture on windows goes to the clipboard; add --copy", req.Mode)
		}
		_, err := sys.Output("explorer.exe", "ms-screenclip:")
		return err
	}

	target := "$bmp.Save('" + escapePS(req.Path) + "')"
	if req.Copy {
		target = "[System.Windows.Forms.Clipboard]::SetImage($bmp)"
	}
	// PowerShell is not DPI-aware, so without SetProcessDPIAware a 250%-scaled
	// 3456x1844 screen reports as 1382x738 and the capture is a scaled corner.
	_, err := sys.PowerShell(`
Add-Type -TypeDefinition 'using System.Runtime.InteropServices; public static class AosDpi { [DllImport("user32.dll")] public static extern bool SetProcessDPIAware(); }'
[AosDpi]::SetProcessDPIAware() | Out-Null
Add-Type -AssemblyName System.Windows.Forms, System.Drawing
$b = [System.Windows.Forms.SystemInformation]::VirtualScreen
$bmp = New-Object System.Drawing.Bitmap $b.Width, $b.Height
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($b.Left, $b.Top, 0, 0, $bmp.Size)
` + target + `
$g.Dispose(); $bmp.Dispose()
`)
	return err
}

// escapePS makes a string safe inside a PowerShell single-quoted literal.
func escapePS(value string) string {
	out := make([]rune, 0, len(value))
	for _, r := range value {
		if r == '\'' {
			out = append(out, '\'')
		}
		out = append(out, r)
	}
	return string(out)
}
