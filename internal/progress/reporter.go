package progress

import (
	"fmt"
	"io"
	"time"
)

// Reporter escreve no terminal do PC o que está chegando: uma linha quando
// um arquivo começa e outra quando termina, com tempo e velocidade.
//
// Ele é mais um consumidor do Hub, ao lado da página web (SSE): os uploads
// não sabem quem está ouvindo. O mapa de inícios é usado só pela goroutine
// que roda Run, então também não precisa de mutex.
type Reporter struct {
	Out io.Writer        // normalmente os.Stdout; nos testes, um bytes.Buffer
	Now func() time.Time // relógio; nil = time.Now (os testes usam um falso)
}

// Run lê eventos até o channel ser fechado.
func (r *Reporter) Run(events <-chan Event) {
	now := r.Now
	if now == nil {
		now = time.Now
	}
	started := map[string]time.Time{} // ID do upload -> quando começou

	for ev := range events {
		t := now()
		start, seen := started[ev.ID]
		if !seen {
			started[ev.ID] = t
			start = t
			if !ev.Done {
				size := ""
				if ev.Total >= 0 {
					size = " (" + HumanBytes(ev.Total) + ")"
				}
				fmt.Fprintf(r.Out, "%s recebendo %s%s\n", t.Format("15:04:05"), ev.Name, size)
			}
		}
		if !ev.Done {
			continue
		}
		delete(started, ev.ID)

		if ev.Err != "" {
			fmt.Fprintf(r.Out, "%s ERRO      %s: %s\n", t.Format("15:04:05"), ev.Name, ev.Err)
			continue
		}
		line := fmt.Sprintf("%s OK        %s  %s", t.Format("15:04:05"), ev.Name, HumanBytes(ev.Bytes))
		// Tempo e velocidade só fazem sentido a partir de 0,1 s; arquivos
		// pequenos chegam tão rápido que o número seria ruído.
		if d := t.Sub(start); seen && d >= 100*time.Millisecond {
			speed := float64(ev.Bytes) / d.Seconds()
			line += fmt.Sprintf(" em %s (%s/s)", d.Round(100*time.Millisecond), HumanBytes(int64(speed)))
		}
		if len(ev.SHA256) >= 12 {
			line += "  sha256 " + ev.SHA256[:12]
		}
		fmt.Fprintln(r.Out, line)
	}
}

// HumanBytes formata um tamanho em bytes: 1536 -> "1.5 KB".
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
