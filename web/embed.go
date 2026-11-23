// Pacote web guarda a página de upload. Com go:embed, o HTML vai dentro do
// binário: quem roda o qrdrop não precisa de nenhum arquivo extra.
package web

import "embed"

// Files contém index.html.
//
//go:embed index.html
var Files embed.FS
