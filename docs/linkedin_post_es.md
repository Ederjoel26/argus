# Publicación de LinkedIn (español, primera versión)

---

Los tramposos han matado más juegos multijugador pequeños que el mal netcode.

Un shooter indie o revivido no puede enviar un driver de kernel ni pagar un anti-cheat comercial, y aun así recibe
los mismos aimbots y speed hacks que un título AAA. Pero tiene el único punto de vista que ningún cheat puede
parchear: el servidor.

Por eso construí **argus**: un motor anti-cheat open source que vive en el servidor, no en la máquina del jugador.
Hoy publico su primera versión y me interesa mucho lo que opinen.

Cómo funciona: el servidor del juego le envía lo que *observó* de cada jugador (posiciones, ángulos de cámara,
disparos, impactos y visibilidad, todo marcado con el tick del servidor) y argus lo valida contra las reglas
físicas y de armas del propio juego, en tiempo casi real:

🏃 Cinemática: velocidad, aceleración, teletransportes, fly hacks y no-clip, calculados con ticks del servidor, no con el reloj del cliente
🎯 Puntería: flicks que giran y se detienen en seco, reacciones por debajo de lo humano, tracking "robótico" sin temblor, silent aim
🔫 Combate: cadencia por arma, máquinas de estado de recarga y cargador, límites de alcance y daño
🧱 Wallhacks: el servidor solo manda las entidades que el jugador podría ver; seguir enemigos a través de paredes se detecta estadísticamente
📈 Una puntuación de anomalía con confianza y decaimiento, mapeada a aviso → shadowban → kick → desconexión

Cualquier juego se conecta con un archivo de configuración: tick rate, velocidades, perfiles de armas, umbrales de
sanción. Nada corre en la PC del jugador.

La implementación de referencia está en Go, solo biblioteca estándar, particionada por jugador para que cada máquina
de estados corra ordenada y sin locks. Cada heurística es una función pura con su prueba de trazas sintéticas al
lado: la matemática se puede auditar y los umbrales ajustar en abierto.

Ya lo tengo integrado en un servidor de un shooter real, donde los impactos se confiaban tal cual los mandaba el
cliente. Por ahora solo reporta, sin sancionar, mientras calibro los umbrales con partidas reales.

Es un comienzo, no una bala de plata. En el roadmap: ingesta por gRPC/UDP, particionado con Kafka/Redis, una
herramienta de replay para calibrar y líneas base aprendidas por arma.

Si administras un juego y quieres aportar un perfil de arma, o crees que puedes armar una traza que engañe a un
check, el repositorio está abierto. Todo feedback, crítico o no, es bienvenido.

🔗 github.com/Ederjoel26/argus

#gamedev #anticheat #opensource #golang #multiplayer #distributedsystems #gamesecurity
