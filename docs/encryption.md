# Cifrado y `ENCRYPTION_KEY`

## Qué protege

`ENCRYPTION_KEY` es una clave de 32 bytes. Se usa solo para lo que **hay que poder leer después** o **comparar sin guardar el valor original**:

| Dato | Cómo se guarda | Por qué |
|---|---|---|
| Secreto del 2FA | Cifrado con AES-256-GCM | La API necesita leerlo para comprobar los códigos |
| Códigos de recuperación del 2FA | HMAC-SHA256 | Solo se comparan; sin la clave, no se pueden probar por fuerza bruta |

Lo demás **no depende de esta clave**, a propósito:

| Dato | Cómo se guarda | Por qué no necesita clave |
|---|---|---|
| Cookie de sesión | Token aleatorio de 256 bits; en la base, su SHA-256 | No contiene datos, así que no hay nada que cifrar. Si se filtra la base, los hashes no sirven para entrar |
| Tokens de recuperación y verificación | Aleatorios de 256 bits; en la base, su SHA-256 | Con 2²⁵⁶ opciones, un hash rápido alcanza |
| Contraseñas | argon2id, con sal aleatoria | La protección es que el algoritmo es lento, no una clave |

Por eso, **cambiar la clave no cierra la sesión de nadie** y solo afecta a los usuarios con 2FA.

## Comparación con el `APP_KEY` de Laravel

Laravel usa una sola clave para todo: cifrar las cookies (incluida la de sesión), `Crypt::encrypt()`, las URL firmadas y el 2FA de Fortify. Acá la clave protege solo el 2FA, así que el daño de una filtración es menor:

| El atacante obtiene… | Laravel (sesiones en la base) | Esta plantilla |
|---|---|---|
| Solo un respaldo de la base | Entra como cualquier usuario con sesión abierta (los ID están en texto plano) | Nada directo; solo puede intentar descifrar contraseñas, y argon2id lo hace muy costoso |
| Solo la clave | Descifra cookies y firma URLs (por ejemplo, verifica cualquier email) | Nada: la clave sola no da acceso a ningún dato |
| La base y la clave | Lee los secretos del 2FA y todos los códigos de recuperación | Lee los secretos del 2FA; los códigos de recuperación requieren fuerza bruta. Igual le falta la contraseña de cada usuario |

## Generar y guardar la clave

```bash
make key
```

En producción, con el binario: `./api generate-key`.

Imprime una clave nueva (32 bytes aleatorios en base64). Copiala en `ENCRYPTION_KEY`. A diferencia de `php artisan key:generate`, no modifica ningún archivo.

**Guardala como cualquier secreto de producción:**
- en el `.env` del servidor, con permisos 600, o en el gestor de secretos que uses;
- **nunca** en el repositorio;
- **con un respaldo aparte del de la base:** si se pierde, los secretos del 2FA no se pueden recuperar.

## Rotar la clave

Conviene rotarla si pudo haberse filtrado (un servidor comprometido, una persona con acceso que se fue) o como práctica periódica.

Cada dato cifrado lleva el identificador de su clave (`52bf1807:…`), así la API sabe con cuál descifrarlo. Durante la rotación conviven la clave nueva y la anterior.

1. **Generá la clave nueva:** `make key`.
2. **Configurá las dos claves y reiniciá la API:**

   ```env
   ENCRYPTION_KEY=<clave nueva>
   ENCRYPTION_PREVIOUS_KEYS=<clave anterior>
   ```

   Desde ese momento, lo nuevo se cifra con la clave nueva, y lo viejo se sigue leyendo con la anterior. Cada vez que un usuario usa su 2FA, su secreto se vuelve a cifrar con la clave nueva.
3. **Corré `make rotate-keys`** (o `./api rotate-keys` en el servidor). Vuelve a cifrar todos los secretos del 2FA que falten e informa:
   - los secretos que no se pudieron descifrar;
   - cuántos usuarios tienen **códigos de recuperación** de la clave anterior;
   - qué claves anteriores ya se pueden quitar.

   Si algo sigue dependiendo de la clave anterior, el comando termina con error.
4. **Códigos de recuperación:** no se pueden volver a calcular sin el código original (por eso son seguros). Los usuarios que el reporte indique tienen que **regenerarlos desde su perfil** (`POST /auth/two-factor/recovery-codes`). Mientras tanto, sus códigos viejos siguen funcionando.
5. **Cuando el reporte diga "Listo"**, quitá la clave de `ENCRYPTION_PREVIOUS_KEYS` y reiniciá.

Si no podés esperar a que todos regeneren sus códigos (por ejemplo, porque la clave se filtró), podés quitarla igual. Esos usuarios pierden sus códigos de recuperación viejos, pero **su app de autenticación sigue funcionando**, porque su secreto ya está con la clave nueva. Pueden generar códigos nuevos desde su perfil.

`ENCRYPTION_PREVIOUS_KEYS` acepta varias claves separadas por comas, por si se superponen dos rotaciones.

## Si se pierde la clave

Los usuarios con 2FA no pueden completar el login: el segundo paso responde `503` con el código `two_factor_unavailable` y el mensaje "contactá al administrador". El log registra la causa y el identificador de la clave que falta.

Si la clave no aparece, hay que desactivarles el 2FA a esos usuarios, para que lo vuelvan a configurar:

```sql
UPDATE users SET totp_secret = NULL, totp_enabled_at = NULL, totp_last_step = NULL WHERE totp_secret IS NOT NULL;
DELETE FROM user_recovery_codes;
```

Para un solo usuario, agregá `AND email = '…'` al `UPDATE` y `WHERE user_id = …` al `DELETE`.

Las sesiones, las contraseñas y el resto de los datos no se ven afectados.
