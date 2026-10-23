# Plano de implementação do go-sip

Análise realizada em 23/10/2026 sobre `main`, commit `8976e56675ff249807c53cd16a7e1e9d584f7dd5`, também referenciado pela tag anotada `v0.2.3`.

Este documento também funciona como checklist de execução. O diagnóstico da seção 1 registra o estado do commit de origem; as caixas das fases registram a árvore de trabalho atual. Itens marcados foram implementados e validados pelos testes indicados. A criação de uma release candidata e a estabilização de `v1.0.0` continuam como decisões do mantenedor, pois exigem uma tag publicada e a política de compatibilidade definitiva.

## 1. Diagnóstico verificável

O projeto é uma biblioteca pequena com sinalização TCP, autenticação MD5 básica e recepção/envio elementar de RTP. Ainda não há base para classificá-la como implementação completa de SIP 2.0 ou pronta para produção.

| Área | Evidência no código atual | Consequência / prioridade |
| --- | --- | --- |
| Registro e eventos | `client/client-sip.go:57` e `:125` consomem o mesmo canal; `RegisterSip` não inicia o dispatcher | Sem `WatchEvents`, o registro pode bloquear; com ele, consumidores disputam respostas. P0 |
| Classificação SIP | `client/client-sip.go:132–177` usa `strings.Contains` | Respostas com `CSeq: ... INVITE` podem ser tratadas como novos INVITEs. P0 |
| Concorrência TCP | `internal/tcpconn.go:62–75` compartilha conexão/canal por endereço; `:125` permite iniciar vários leitores | Clientes distintos no mesmo servidor podem consumir mensagens uns dos outros. P0 |
| RTP | `internal/rtp.go:19–25` mantém socket, sequência, timestamp e estado de início globais; `:121–133` altera contadores sem proteção | Chamadas interferem entre si; riscos de data races identificados por inspeção. P0 |
| Parser de rede | `internal/tcpconn.go:158–190` não limita headers/corpo, ignora erro de tamanho e não reconhece header compacto | Alocação excessiva e framing inválido com entrada não confiável. P0 |
| Ciclo de vida | Ausência de `Close`, contexto público e cancelamento do dispatcher; erros de envio ignorados em vários métodos | Bloqueios, recursos persistentes e falhas invisíveis ao consumidor. P0 |
| REGISTER | CSeq fixo em 1/2; identificadores gerados com segundos; sem renovação/desregistro | Colisões, correlação frágil e expiração após o registro inicial. P1 |
| Autenticação | `internal/auth.go:13–18` implementa MD5 sem qop; `ParseAuth` extrai somente nonce/realm/opaque | Sem `qop`, `nc`, `cnonce`, `stale`, 407 ou SHA-256. P1 |
| Diálogo | `Build180Ringing` e `Build200OKInvite` geram tags independentemente; Contact contém `asterisk` e porta fixa | Inconsistência de diálogo, identidade e endereço de retorno. P1 |
| BYE | `client/client-sip.go:177–182` apenas cancela RTP | Não envia 200 OK nem verifica qual diálogo está terminando. P1 |
| SDP/mídia | SDP fixo em `internal/transport.go:191–204`; destino de envio vem de pacote recebido | Não negocia oferta/resposta, anuncia DTMF não implementado e depende de mídia remota inicial. P1 |
| RTP avançado | Parser presume header de 12 bytes; envio reutiliza SSRC remoto e incrementa timestamp por bytes | Extensões/CSRC/padding e codecs diferentes de G.711 não são tratados corretamente. P1 |
| Rede | IPv4 escolhido pela primeira interface; Via fixa 5060; Contact de REGISTER usa porta do servidor | Problemas em múltiplas interfaces, portas efêmeras, NAT e IPv6. P1 |
| Reconexão | Retry de 1/2/3 segundos; falha em `Send` invalida conexão e retorna | Backoff linear, não exponencial; não há garantia de reenvio ou recuperação da transação. P1 |
| Documentação/API | README usa `internal.InviteData`/`internal.AudioData`, callback antigo e histórico v1.0.0 | Exemplos incompatíveis com a API pública atual e versões reais. P1 |
| Exemplos | Arquivos são `package example`, inclusive o que declara `main`; Oto está no mesmo pacote | Exemplos não são comandos independentes; dependência de áudio afeta o build geral. P1 |
| Qualidade/publicação | Sem `_test.go`, `.github`, changelog ou licença na árvore analisada | Não há validação automatizada, política documentada de releases ou licença explícita. P1 |

### Verificações executadas

- `gofmt -l client internal types example`: nenhum arquivo apontado.
- `go test ./...`: falhou no pacote `example`, porque `pkg-config` não encontrou ALSA (`alsa.pc`). Os pacotes principais foram compilados e indicaram ausência de testes.
- `go vet ./...`: também bloqueado pela dependência ALSA do exemplo.
- `go test -race ./client ./internal ./types`: concluiu, mas todos os pacotes informaram `[no test files]`; isso **não valida concorrência em execução**.
- `go vet ./client ./internal ./types`: concluiu sem diagnósticos.
- Ambiente local: `go1.26.8-X:nodwarf5 linux/amd64`; o módulo declara Go `1.22.2`. A versão mínima ainda precisa ser testada em CI.
- Tags locais: `v0.2.0`, `v0.2.1`, `v0.2.2`, `v0.2.3`; `v0.2.3^{}` e HEAD resolvem para o mesmo commit.
- Consultas à API do GitHub retornaram zero releases e zero workflows no repositório `jvrs2812/go-sip`.
- Não foram realizados testes de interoperabilidade com PBX real nem benchmarks. Números de desempenho e garantia de segurança concorrente do README não foram confirmados.

## 2. Corrigir a especificação antes de ampliar o código

Substituir o status binário atual por **implementado e testado**, **parcial**, **planejado** e **experimental**, com links para testes e exemplos. REGISTER, INVITE recebido, OPTIONS, BYE, TCP e RTP devem ser descritos como parciais até seus critérios de aceite passarem.

Correções normativas verificadas nas fontes oficiais:

| Texto atual | Correção e tratamento no plano |
| --- | --- |
| RFC 4169 — chamadas de emergência | A [RFC 4169](https://www.rfc-editor.org/info/rfc4169/) trata de Digest AKA v2. Preservar dois itens distintos: AKA v2 e o requisito funcional de chamadas de emergência, cujo perfil interoperável será especificado em F9. |
| RFC 5939 — SIPS | A [RFC 5939](https://www.rfc-editor.org/info/rfc5939/) trata de negociação de capacidades SDP. Implementá-la em F9; SIPS será tratado em F5 com base na [RFC 5630](https://www.rfc-editor.org/info/rfc5630/). |
| RFC 7044 — atualização de RFC 3960 | A [RFC 7044](https://www.rfc-editor.org/info/rfc7044/) trata de `History-Info`. Separar histórico de encaminhamento em F9 e o requisito de early media em F4. |
| RFC 3265 — eventos | Usar a [RFC 6665](https://www.rfc-editor.org/info/rfc6665/), que substitui a 3265, como base de SUBSCRIBE/NOTIFY. |
| RFC 2617 / SHA-256 | Corrigir a explicação MD5 para HA1/HA2 e resposta Digest; ampliar a implementação conforme a [RFC 8760](https://www.rfc-editor.org/info/rfc8760/). |

Remover as afirmações sem evidência de conformidade completa, desempenho fixo, logging estruturado e 100% de segurança concorrente. Documentar que o compartilhamento atual de uma conexão por endereço não representa um pool completo com isolamento e política de ciclo de vida.

## 3. Arquitetura proposta

Manter o módulo `github.com/jvrs2812/go-sip` e evoluir por componentes com responsabilidades explícitas:

- `sip`: mensagens, URIs, headers, parser/serializer e erros de protocolo.
- `transport`: contrato de transporte e implementações TCP/UDP/TLS/WS/WSS; SCTP em adaptador específico.
- `transaction`: correlação, estados, timers e retransmissão de transações cliente/servidor.
- `dialog`: identificação por Call-ID e tags, route set, CSeq e estado das chamadas.
- `auth`: estratégias de autenticação, separadas do transporte.
- `sdp`: oferta/resposta e negociação de mídia.
- `media`: sessão RTP/RTCP por chamada, codecs, DTMF e NAT traversal.
- `client`: fachada pública, configuração, registro, chamadas e callbacks/eventos.

Essas são fronteiras de responsabilidade; usar subpacotes `internal/` enquanto não houver necessidade de API pública. Preservar `types` durante a migração. Tipos usados por consumidores não podem exigir importação de `internal`.

Proposta inicial de API: `NewClient(Config)`, `Register(ctx) error`, `Unregister(ctx) error`, `Invite(ctx, target) (*Call, error)`, `Call.Accept(ctx) error`, `Call.Hangup(ctx) error`, `Call.SendAudio(...) error` e `Close() error`. A sessão deve permitir iniciar mídia a partir do SDP, sem depender de receber primeiro um pacote RTP.

Manter wrappers obsoletos para as funções existentes quando viável, documentar mudanças incompatíveis nas versões 0.x e estabilizar o contrato antes da v1.0. Um único leitor por conexão entrega mensagens às transações/diálogos corretos. Callbacks recebem eventos tipados por filas limitadas, com política documentada de bloqueio/descarte e encerramento.

## 4. Cobertura completa das pendências do README

Itens repetidos entre “Protocol Features”, “Authentication”, “Network” e “Future Roadmap” foram consolidados abaixo, sem excluir funcionalidades.

| Requisito | Situação atual | Entrega |
| --- | --- | --- |
| REGISTER, renovação e remoção | Registro básico e frágil | F2 |
| INVITE recebido e enviado; ACK; BYE; CANCEL; OPTIONS | INVITE recebido, BYE e OPTIONS parciais; demais ausentes | F3 |
| PRACK / respostas provisórias confiáveis | Ausente | F3 |
| Route, Record-Route e outbound proxy | Ausentes | F3 + F5 |
| TCP, pool, reconexão e backoff | Parciais | F1 + F5 |
| UDP para sinalização | Ausente; UDP atual atende somente RTP | F3 |
| RFC 3263, SRV, descoberta e failover | Ausentes | F5 |
| IPv6 | Ausente no tratamento SIP/SDP e no listener RTP | F5 |
| TLS e SIPS | Ausentes | F5 |
| SHA-256 | Ausente | F2 |
| SUBSCRIBE, NOTIFY e presença | Ausentes | F6 |
| PUBLISH | Ausente | F6 |
| MESSAGE | Ausente; delimitar inicialmente mensagens SIP fora de sessão | F6 |
| INFO | Ausente | F6 |
| REFER / transferência | Ausente | F6 |
| ICE, STUN e TURN | Ausentes | F7 |
| DTLS / mídia protegida | Ausentes | F7; especificar perfil de mídia, separado de SIP sobre TLS |
| DTMF envio/recepção | Anunciado no SDP, sem implementação | F4 |
| Hold/resume | Ausente | F4 |
| G.711, callbacks, reprodução e envio de áudio | Decodificação apenas no exemplo; RTP básico no núcleo | F4 |
| G.722, GSM-FR, Opus, iLBC e SILK | Ausentes | F8, com entrega e teste de cada codec |
| Conference bridges | Ausente | F8 |
| WebSocket, incluindo WSS | Ausente | F9 |
| SCTP | Ausente | F9 |
| SIP Identity | Ausente | F9 |
| OAuth 2.0 | Ausente | F9 |
| RFC 4169 / AKA v2 e requisito de emergência | Ausentes; README mistura conceitos | F9, especificações separadas |
| RFC 5939 / capacidades SDP | Ausente | F9 |
| RFC 7044 / History-Info | Ausente | F9 |
| Early media mencionado indiretamente pela referência à RFC 3960 | Não existe estado/negociação próprios | F4 |
| Exemplos, documentação, testes, benchmarks e logging | Parciais ou ausentes | F0, F1 e F10; atualização em cada entrega |
| GitHub Actions e release por tags | Ausentes | F0, com expansão por fase |

## 5. Fases de implementação e critérios de aceite

### F0 — Build reproduzível, documentação fiel e automação inicial

Dependências: nenhuma. Prioridade: imediata.

- [x] Corrigir README: API real, `types`, callback com `*Client`, `OnStartCall`, `SendAudio`, versões 0.2.x e limites atuais.
- [x] Transformar exemplos em comandos separados (`example/register`, `example/receive`, `example/player`), com configuração por flags/ambiente e encerramento por sinal/contexto.
- [x] Isolar a reprodução Oto em um módulo próprio; núcleo e exemplos sem áudio compilam sem ALSA, e a dependência do player está documentada e validada em job próprio.
- [x] Executar `go mod tidy` após a reorganização e corrigir dependências diretas/indiretas.
- [x] Introduzir primeiros testes úteis: vetores Digest, serialização e framing SIP, RTP mínimo e reprodução dos bloqueios de registro.
- [x] Criar CI e workflow de release descritos na seção 6, além de `CHANGELOG.md`, `CONTRIBUTING.md` e `docs/releasing.md`.
- [x] Registrar a escolha de licença pelo mantenedor antes de adicionar `LICENSE`; a ausência de decisão está registrada sem presumir uma licença.

Aceite: checkout limpo compila e testa sem hardware de áudio; job separado compila o player com suas dependências; exemplos são executáveis; pipeline valida o mesmo commit da tag; status do README corresponde às evidências. Primeira release de manutenção candidata: `v0.2.4`, se as alterações preservarem compatibilidade.

### F1 — Parser, transporte e ciclo de vida confiáveis

Dependências: F0.

- [x] Criar parser tipado com start-line, headers case-insensitive/repetidos/compactos, URIs, corpo binário, limites configuráveis e validação de Content-Length.
- [x] Testar mensagens fragmentadas e concatenadas em TCP, CRLF de keep-alive, EOF parcial e tamanhos inválidos; adicionar fuzzing.
- [x] Remover a classificação por substring e unificar builders em serializer que preserve headers necessários.
- [x] Garantir um dispatcher por conexão, correlação de respostas e isolamento entre clientes que compartilham servidor.
- [x] Adicionar contexto, deadlines, `Close` idempotente, retorno de erros e encerramento de filas/goroutines.
- [x] Retirar globais de RTP; estado/socket/contadores/cancelamento pertencem à sessão da chamada.
- [x] Implementar IDs aleatórios, tratamento de escrita parcial e logging configurável com remoção de Authorization, tokens e credenciais.

Aceite: testes reproduzem e eliminam disputa por respostas, leitores duplicados e interferência entre duas chamadas; suíte com `-race` executa concorrência real; cancelamento libera sockets/goroutines; entrada malformada não provoca panic ou alocação sem limite.

### F2 — Registro e autenticação completos no perfil suportado

Dependências: F1.

- [x] Máquina de estados de registro com Call-ID persistente, CSeq crescente, branch por transação e parâmetros locais corretos.
- [x] Registro com/sem desafio, 401/407, expiração efetivamente concedida, renovação antecipada e desregistro com Expires zero.
- [x] Tratar rejeição de credenciais, timeout, 423/Min-Expires, stale nonce e limite de tentativas sem loops infinitos.
- [x] Digest MD5 para compatibilidade e SHA-256; negociar algoritmo/qop, `cnonce`/`nc`, opaque e desafios múltiplos.
- [x] Revalidar registro após perda de conexão, com backoff exponencial limitado e jitter.

Aceite: servidor SIP simulado e PBX de integração validam registro, renovação, remoção e autenticação; falhas chegam como erros/eventos; senhas e tokens não aparecem nos logs.

### F3 — Transações, diálogos, chamadas e UDP

Dependências: F1 e F2.

- [x] Implementar estados de transações INVITE/não INVITE, timeouts, respostas duplicadas e retransmissões apropriadas ao transporte.
- [x] Completar INVITE de entrada/saída, respostas provisórias/finais, ACK de sucesso/falha, rejeição, BYE de ambos os lados e CANCEL.
- [x] Identificar diálogos por Call-ID e tags; manter tag local estável, remote target, route set e CSeq; tratar fork e respostas tardias.
- [x] Completar OPTIONS de entrada/saída com capacidades reais; métodos desconhecidos recebem resposta apropriada.
- [x] Implementar PRACK com negociação `100rel`, RSeq/RAck, retransmissão exponencial da resposta provisória e cancelamento do timer após confirmação.
- [x] Processar Route/Record-Route e permitir outbound proxy configurável.
- [x] Adicionar transporte UDP com limites, retransmissão, deduplicação e correlação integrada às transações.

Aceite: cenários automatizados cobrem chamadas nos dois sentidos, CANCEL antes/depois de resposta final, BYE respondido com 200, chamada desconhecida, perda/duplicação UDP, PRACK e chamadas simultâneas. Validar os detalhes contra a [RFC 3261](https://www.rfc-editor.org/info/rfc3261/) e suas atualizações aplicáveis.

### F4 — SDP, áudio bidirecional, DTMF e hold/resume

Dependências: F3; isolamento de mídia de F1.

- [x] Parser e negociação SDP: endereço/porta remotos, payloads dinâmicos, codecs, clocks, canais, direção e rejeição de mídia incompatível.
- [x] Abrir socket de mídia antes de aceitar chamada; falha de bind impede anúncio de uma porta indisponível.
- [x] Completar RTP: versão, CSRC, extensões, padding, SSRC local, sequência, timestamp baseado em amostras e pacing por codec; adicionar RTCP e métricas básicas.
- [x] Implementar buffer de jitter limitado e tratamento de perda/reordenação.
- [x] Disponibilizar G.711 encode/decode e interface de codec; callbacks com metadados suficientes.
- [x] Implementar DTMF por telephone-event, incluindo duração, término e deduplicação; negociar payload em vez de fixar 101.
- [x] Hold/resume com renegociação de SDP e tratamento de ofertas simultâneas; early media com estado separado de chamada confirmada.
- [x] Definir `OnStartCall` como evento de primeira mídia, preservado como alias legado de `OnFirstMedia`; `OnCallEstablished` representa a confirmação SIP.

Aceite: áudio G.711 bidirecional pode começar sem RTP remoto prévio; testes de pacotes e duas chamadas em paralelo passam; DTMF, pausa/retomada e early media interoperam com PBX; comportamento RTP validado contra a [RFC 3550](https://www.rfc-editor.org/info/rfc3550/).

### F5 — Descoberta, failover, IPv6 e TLS

Dependências: F3 e F4.

- [x] Resolver destino por configuração explícita ou procedimentos DNS com NAPTR/SRV/A/AAAA, prioridades, pesos, cache e injeção de resolver para testes.
- [x] Failover entre destinos com prazo total limitado; evitar repetir cegamente operações cujo resultado é desconhecido.
- [x] Selecionar endereço local coerente com a rota, permitir bind/advertised address e usar `net.JoinHostPort`/URIs IPv6 corretamente.
- [x] Completar IPv6 em transporte, Via/Contact, SDP e RTP; suportar dual stack onde disponível.
- [x] TLS com validação de certificado/hostname, confiança configurável e política SIPS sem downgrade silencioso.
- [x] Definir o ciclo de vida de conexão por cliente e identidade, reutilizando a conexão dentro do cliente e preservando isolamento entre contas; o projeto não mantém um pool global inseguro.

Aceite: DNS/failover testados de forma determinística, cenários IPv4/IPv6 locais e TLS válido/inválido; múltiplas contas no mesmo servidor permanecem isoladas. Base de descoberta: [RFC 3263](https://www.rfc-editor.org/info/rfc3263/).

### F6 — Eventos, presença, mensagens e transferência

Dependências: F3 e F5.

- [x] SUBSCRIBE/NOTIFY: criação, atualização, expiração, cancelamento, estado da assinatura e validação de pacote de evento.
- [x] Presença: PIDF interoperável, estado tipado e papéis de subscriber/publisher documentados.
- [x] PUBLISH: publicação inicial, refresh, remoção e controle de versão da publicação.
- [x] MESSAGE: enviar/receber conteúdo e tipo MIME, limites e respostas de erro; sessões MSRP permanecem fora deste escopo.
- [x] INFO: despachar conteúdo negociado e rejeitar tipos não suportados; DTMF via INFO disponível quando configurado.
- [x] REFER: transferência cega e assistida, acompanhamento por NOTIFY e associação entre diálogos.

Aceite: cada método tem cenário de sucesso, rejeição, timeout e limpeza; presença expira corretamente; transferência termina com estado coerente nos dois diálogos.

### F7 — NAT traversal e proteção de mídia

Dependências: F4 e F5.

- [x] STUN, candidatos ICE, verificações de conectividade, seleção de pares e ICE restart.
- [x] TURN com autenticação, renovação e liberação das alocações pelo agente Pion; teste relay-only com servidor TURN local e tráfego pelo par selecionado.
- [x] Implementar perfil DTLS-SRTP com negociação de fingerprints, papéis, chaves e proteção SRTP/SRTCP.
- [x] Documentar separadamente TLS para sinalização e proteção da mídia; delimitar qualquer requisito adicional de SIP sobre DTLS antes de tratá-lo como transporte suportado.
- [x] Testar mídia atrás de NAT, relay obrigatório, expiração de credenciais, fingerprint inválido e encerramento.

Aceite: chamadas bidirecionais funcionam nos cenários diretos e via relay; captura de teste demonstra mídia protegida; falhas de autenticação interrompem estabelecimento sem fallback silencioso.

### F8 — Codecs adicionais e conferência

Dependências: F4; F7 para cenários com mídia protegida.

- [x] Selecionar implementação/manutenção/dependências e termos de distribuição dos codecs integrados e registrar as decisões em `docs/codecs.md`.
- [x] Implementar e testar separadamente **G.722, GSM-FR, Opus, iLBC e SILK**, sem considerar SILK concluído apenas por haver Opus.
  - [x] G.722 puro em Go, encoder/decoder stateful, payload RTP estático 9 e negociação SDP.
  - [x] GSM-FR opcional via `libgsm`, encoder/decoder de frames de 20 ms, payload RTP estático 3 e anúncio condicionado ao build tag `gsm`.
  - [x] Opus opcional via `libopus`, encoder/decoder/PLC, clock RTP de 48 kHz, payload dinâmico e anúncio condicionado ao build tag `opus`.
  - [x] iLBC puro em Go, modos de 20/30 ms, encode/decode, concealment de perda e negociação SDP por modo.
  - [x] SILK standalone opcional, separado de Opus, com encoder/decoder de frames, clocks de 8/12/16/24 kHz e payload dinâmico condicionado ao build tag `silk`.
- [x] Para cada codec: encoder/decoder quando disponíveis no perfil, formato RTP, negociação SDP, clock, frame size e vetores locais; interoperabilidade externa permanece como critério de estabilização F10.
- [x] Manter integrações que exigem CGO ou bibliotecas nativas isoladas; ampliar matriz de build conforme os backends selecionados.
- [x] Conference bridge: modelo de participantes, mistura em PCM comum, reamostragem linear, ganho/clipping, mute, entrada/saída e limites de recursos.

Aceite: cada codec anunciado realmente envia/recebe áudio com testes; conferência de pelo menos três participantes mantém áudio e isolamento; benchmarks informam consumo e limites medidos. Pesquisa sem adaptador funcional não encerra um item de codec.

### F9 — Transportes e extensões avançadas

Dependências: F2, F5 e F6; capacidades SDP dependem de F4.

- [x] WebSocket/WSS: framing, subprotocolo SIP, limites, keep-alive, reconexão e interoperabilidade de sinalização em peers locais; integração de mídia em navegador permanece fora do transporte SIP.
- [x] SCTP: backend de SCTP do kernel em Linux exceto 386, erro explícito nas demais plataformas, associação limitada por timeout, envio/recepção em teste Linux e compilação cruzada para Windows/macOS.
- [x] SIP Identity: assinatura/verificação ES256, provider de certificados, validade temporal e falhas de validação, em perfil experimental explícito.
- [x] OAuth 2.0: provider de token injetável, obtenção/renovação delegadas à aplicação, desafio de autenticação SIP e remoção de tokens dos logs.
- [x] AKA v2 (RFC 4169): provider de SIM/ISIM especializado, preferência contra downgrade, derivação HMAC da senha e chaves, e vetores de teste; segredos de operadora permanecem fora da biblioteca.
- [x] Chamadas de emergência: perfil restrito a endpoints de laboratório, URNs RFC 5031, roteamento, localização opcional e respostas esperadas; serviços reais são rejeitados nos testes.
- [x] RFC 5939: preservação de atributos de capacidade, parsing de `csup`/`creq`/`tcap`/`acap`/`pcfg`/`acfg`, seleção de configuração e fallback testados.
- [x] RFC 7044: parser/serializer de History-Info, cadeia de encaminhamento e política configurável de preservação, anonimização ou remoção.

Aceite: cada extensão tem perfil documentado e ao menos um peer de integração verificável, além dos testes de falha. Dependências externas e suporte por plataforma devem constar no README. Esses itens continuam no backlog até a implementação efetiva; uma decisão de arquitetura não equivale à entrega.

### F10 — Estabilização e entrega completa

Dependências: todas as fases anteriores para declarar todo o README entregue.

- [x] Revisar API, compatibilidade e documentação Go; compilar exemplos de consumidor em módulo externo, sem acesso a `internal`.
- [x] Executar cenários com servidor simulado e Asterisk 22.11.0 em imagem fixada; manter matriz de compatibilidade real.
- [x] Rodar fuzzing prolongado semanal, stress de chamadas simultâneas, testes de recuperação e vazamentos, e análise de vulnerabilidades.
- [x] Publicar benchmarks reproduzíveis de registro, parser, chamadas e áudio, com ambiente e tamanho da carga.
- [x] Consolidar changelog, guia de migração, matriz de suporte e exemplos dos grupos de recursos públicos.
- [x] Preparar releases candidatas e `v1.0.0` com gates de tag/SHA, validação, integração, notas, pré-release/latest e smoke test no Go proxy.
- [x] Publicar a candidata `v0.3.0-rc.1` após escolher a licença MIT e verificar o commit de release.
- [ ] Concluir `v1.0.0` após estabilizar a API e definir a política definitiva de compatibilidade.

Aceite: todos os itens da seção 4 vinculados a código, testes e documentação; nenhuma caixa marcada apenas porque um header é gerado. É possível lançar uma v1 de escopo menor, mas isso não significaria concluir este plano integral.

## 6. GitHub Actions e publicação baseada em tags

### Arquivos implementados

| Arquivo | Responsabilidade |
| --- | --- |
| `.github/workflows/ci.yml` | PRs e pushes em `main`; chamar a validação compartilhada |
| `.github/workflows/validate.yml` | Workflow reutilizável via `workflow_call`; validar um SHA explícito e devolver os checks necessários |
| `.github/workflows/release.yml` | `push.tags: ['v*']` e execução manual com tag; validar e publicar |
| `.github/workflows/security.yml` | Análise de vulnerabilidades periódica e manual; ferramentas em versões fixadas |
| `.github/workflows/integration.yml` | Cenários PBX/SIPp, NAT e mídia conforme as fases; chamado como requisito das releases pertinentes |
| `.github/dependabot.yml` | Atualizações de Go modules e Actions |
| `.github/release.yml` | Categorias das notas de release |
| `scripts/validate-release.*` | Validação SemVer, resolução imutável da tag e verificação de política de publicação |
| `docs/releasing.md` | Procedimento de novas versões, pré-releases, versões antigas e recuperação de falhas |

### Pipeline de CI

1. Usar `contents: read`, Actions fixadas por SHA revisado, cache por `go.sum`, timeouts e cancelamento de execuções obsoletas do mesmo PR.
2. Testar o Go mínimo declarado no `go.mod` e a versão estável escolhida na implementação; manter a matriz atualizada. Não considerar o toolchain local como prova de compatibilidade com Go 1.22.2.
3. Executar verificação de `gofmt`, `go mod verify`, `go vet`, `go test` e `go build`. Validar `go mod tidy` em checkout descartável e falhar se alterar `go.mod`/`go.sum`.
4. Linux: `go test -race -count=1 -coverprofile=coverage.out ./...` após o isolamento do player. Executar build/test também em Windows e macOS para o núcleo.
5. Compilar exemplos de áudio em job separado com `-tags audio`; instalar `libasound2-dev`/`pkg-config` no Linux. Reprodução real exige ambiente apropriado e não deve bloquear a CI sem dispositivo.
6. Introduzir fuzz smoke tests e verificação de vulnerabilidades; ampliar os cenários de integração com cada funcionalidade. Cobertura inicial serve como baseline; exigir regressões testadas antes de impor um percentual arbitrário.
7. Publicar relatórios de teste/cobertura como artefatos de CI; executar lint dos workflows. Configurar checks obrigatórios e regras para `main`/tags no GitHub durante a implantação, não apenas arquivos YAML.

### Pipeline de release

Fluxo implementado: **tag enviada → validação da tag/SHA → mesma suíte de qualidade → integração exigida pelo escopo → notas/artefatos → GitHub Release → verificação do módulo publicado**.

1. Disparar no push de tags `v*`, com validação SemVer estrita posterior. Aceitar `vX.Y.Z` e pré-releases como `v0.3.0-rc.1`; rejeitar referências inesperadas e versões incompatíveis com o module path.
2. Fazer checkout com histórico/tags completos. Resolver a tag anotada ou leve para commit uma única vez e usar esse SHA em todos os jobs. Confirmar novamente a associação tag/SHA antes da publicação.
3. Exigir que a tag pertença ao histórico permitido (`main` inicialmente). Se houver branches de manutenção, explicitá-las na política. Nunca testar `main` no lugar do commit da tag.
4. Chamar a validação compartilhada com o SHA. O job de publicação deve depender de todos os checks exigidos; um build isolado não substitui os testes.
5. Usar `concurrency` por tag e `cancel-in-progress: false` na release. Somente o job que cria a release recebe `contents: write`, usando `GITHUB_TOKEN`.
6. Gerar notas com tag anterior adequada, mudanças incompatíveis, instrução `go get`, limitações e matriz de suporte. Dados dinâmicos entram por variáveis/arquivos; evitar interpolação direta em scripts.
7. Criar release com `gh release create`, `--verify-tag` e notas automáticas ou arquivo revisável. O [CLI oficial](https://cli.github.com/manual/gh_release_create) oferece verificação de tag e classificação de pré-release.
8. Pré-release recebe `prerelease=true` e nunca `latest`. Release histórica não substitui a mais nova. Para uma release estável, comparar SemVer antes de marcar `latest`.
9. Reexecução: se já publicada com a mesma tag/SHA e conteúdo esperado, concluir sem duplicar; se for draft da mesma execução, completar; divergências devem falhar claramente. Não sobrescrever uma release existente silenciosamente.
10. Para esta biblioteca, entregar código-fonte, notas e documentação de consumo. Não há aplicativo principal para empacotar por GOOS/GOARCH. GoReleaser só passa a ser necessário se houver CLI/distribuição binária; fontes do GitHub já têm arquivos automáticos. Caso sejam gerados anexos próprios, adicionar checksums e manifesto com SHA/versão/toolchain.
11. Verificar a versão com `GOPROXY=proxy.golang.org go list -m github.com/jvrs2812/go-sip@<tag>` e executar smoke test em módulo consumidor limpo. Tratar atraso de indexação com retry limitado e registrar falha recuperável, sem recriar tags.

Em Go, publicar a tag torna a versão consumível independentemente de existir uma GitHub Release; a release adiciona notas e anexos. Portanto, os checks obrigatórios antes da criação da tag também importam: falhar no workflow impede a GitHub Release, mas não retira a tag já exposta. Não mover tags publicadas; corrigir com nova versão e, se necessário, `retract`. Ver [publicação de módulos](https://go.dev/doc/modules/publishing).

Os gatilhos seguem a documentação de [eventos do GitHub Actions](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows). Uma tag criada por workflow usando o próprio `GITHUB_TOKEN` não deve depender de outro workflow de `push` para publicar: chamar a lógica reutilizável na mesma execução ou usar disparo explícito adequado, conforme as [restrições de eventos do token](https://docs.github.com/en/actions/concepts/security/github_token).

### Tags existentes e validação da automação

- `workflow_dispatch` receberá uma tag existente e a resolverá para SHA. Assim é possível publicar releases históricas sem apagar ou recriar as tags; adicionar um workflow hoje não executa retroativamente os pushes antigos.
- O workflow manual ficará em `main` e fará checkout do código histórico. A implementação deverá separar a lógica de validação atual dos scripts eventualmente ausentes na versão antiga.
- Tags antigas devem passar por validação compatível com o código daquela época, incluindo ALSA quando necessária. Não afirmar que uma versão antiga passou por testes que ainda não existiam. Se falhar, relatar e publicar uma nova versão corrigida, sem contornar os checks silenciosamente.
- O script `scripts/test-release-validation.sh` valida sem publicar: tag válida, tag inválida, pré-release, versão principal incompatível e commit fora do histórico de `main`. Falha de teste impede o job de publicação; rerun, tag histórica, mudança de SHA e indexação atrasada são tratados pelo workflow e por `docs/releasing.md`.
- No repositório real, começar por uma pré-release aprovada pelo processo normal de manutenção; verificar notas, flag de pré-release, versão do módulo e ausência de binários indevidos.
- Manter tags `v0.x` durante a evolução. A partir de `v2`, alinhar module path e imports com `/v2`, conforme o [fluxo de versionamento Go](https://go.dev/doc/modules/release-workflow).

## 7. Ordem sugerida de PRs e marcos

| Marco | PRs em ordem | Resultado verificável |
| --- | --- | --- |
| M0 | 1. README/exemplos/build; 2. testes iniciais; 3. CI; 4. release por tag | Projeto compilável e publicação reproduzível |
| M1 | 5. parser/serializer; 6. dispatcher/contextos; 7. RTP por sessão; 8. registro/Digest | Base sem bloqueios conhecidos e autenticação testada |
| M2 | 9. transações/diálogos; 10. chamadas/ACK/BYE/CANCEL; 11. UDP/PRACK/rotas; 12. SDP/mídia/DTMF/hold | Chamadas completas e áudio bidirecional |
| M3 | 13. DNS/failover/IPv6; 14. TLS/SIPS; 15. eventos/presença; 16. MESSAGE/INFO/REFER | Interoperabilidade e funcionalidades de comunicação |
| M4 | 17. ICE/STUN/TURN; 18. mídia protegida; 19+. um PR por codec; conferência em série própria | Mídia avançada com limites medidos |
| M5 | Séries próprias para WS/SCTP, Identity/OAuth/AKA, emergência, capacidades SDP e History-Info | Todos os itens avançados implementados e validados |
| M6 | Revisão de compatibilidade, testes prolongados, documentação e releases candidatas | Escopo integral pronto para estabilização |

Cada PR deve incluir regressão ou cenário interoperável, atualização do status documental e critério de aceite da fase. Os números indicam sequência lógica, não tamanho fixo: transações, mídia e extensões devem ser divididas em mudanças revisáveis.

Não há base para prometer prazo fechado para todo o README: conferência, NAT traversal, codecs e autenticação avançada ampliam muito o escopo atual. Estimar cada série após fechar o contrato público, selecionar backends e comprovar o ambiente de integração. O primeiro incremento recomendado é M0; depois, corrigir a base em M1 antes de adicionar novos métodos.

## 8. Decisões a fechar durante a execução

- Licença e política de compatibilidade; preservar o mínimo Go atual até uma mudança explícita.
- Perfil de produto: biblioteca de user agent como núcleo; servidor completo, proxy e B2BUA não são implicitamente entregues por implementar métodos SIP.
- Dependências/backends para codecs e transportes especiais, plataformas suportadas e eventual uso de CGO.
- Perfis e peers para Identity, OAuth, AKA v2 e emergência. Isso não bloqueia M0–M4, mas precisa estar definido antes das implementações dependentes.
- Limites padrão de mensagens, filas, chamadas, memória e duração dos testes; validar com medições.

Conclusão de cada requisito exige implementação, testes relevantes, exemplo quando houver API pública e documentação fiel. Todas as entregas devem passar pelo mesmo processo de validação e release por tag.
