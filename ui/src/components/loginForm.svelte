<script lang="ts">
    import '../styles/loginForm.css';
    import { getClient } from '../lib/grpcClient';
  
    let email = '';
    let password = '';
    let message = '';
    let isLoading = false;
  
    async function handleLogin() {
      if (!email || !password) {
        message = "Veuillez entrer un nom d'utilisateur et un mot de passe.";
        return;
      }
  
      try {
        const client = getClient();
        const response = await client.login({ username: email, password });
        console.log(response);
      } catch (error) {
        console.error("Login error: ", error);
      }
    }
  </script>
  
  <div class="login-form-container">
    <h1 class="login-form-title">Connection</h1>
  
    <div class="login-form-group">
      <label for="email" class="login-form-label">Email</label>
      <input id="email" type="email" class="login-form-input" bind:value={email} />
    </div>
  
    <div class="login-form-group">
      <label for="password" class="login-form-label">Password</label>
      <input id="password" type="password" class="login-form-input" bind:value={password} />
    </div>
  
    <button class="login-form-button" on:click={handleLogin} disabled={isLoading}>
      {isLoading ? "Loading..." : "Log In"}
    </button>
  
    <div class="login-form-footer">
      <a href="#" class="login-form-forgot">Forgot Password ?</a>
    </div>
  
    {#if message}
      <p class="login-form-message">{message}</p>
    {/if}
  </div>
  