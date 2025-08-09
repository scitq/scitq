import type { RpcOptions } from "@protobuf-ts/runtime-rpc";
import { get } from 'svelte/store';
import {isLoggedIn } from '../lib/Stores/user';
import { client } from './grpcClient';
import { getUser } from "./api";

/**
 * Authenticates a user with the provided credentials and manages user session.
 * 
 * @param {string} username - The user's login username
 * @param {string} password - The user's login password
 * @returns {Promise<any>} Promise that resolves with:
 *   - The login response data if JSON content is returned
 *   - null if response has no JSON content
 * @throws {Error} Throws an error when:
 *   - HTTP request fails (non-OK response)
 *   - Authentication token is missing after login
 *   - Network or server errors occur
 * 
 * @example
 * try {
 *   const loginData = await getLogin('user123', 'password123');
 *   // Handle successful login
 * } catch (error) {
 *   // Handle login error
 * }
 */
export async function getLogin(username: string, password: string) {
  try {
    const response = await fetch('https://alpha2.gmt.bio/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
      credentials: 'include'
    });

    if (!response.ok) {
      throw new Error(`Login failed with status: ${response.status}`);
    }

    // 1. First obtain the authentication token
    const tokenCookie = await getToken();
    if (!tokenCookie) throw new Error('Token not found after login');
    
    // 2. Update application state stores
    isLoggedIn.set(true);

    // 3. Fetch and store user information
    const user = await getUser(tokenCookie);
    if (user?.username) {
      localStorage.setItem('username', user.username);
    }

    // Skip JSON parsing for empty responses
    const contentType = response.headers.get('content-type');
    if (contentType && contentType.includes('application/json')) {
      return await response.json();
    }
    
    return null; // Return null for non-JSON responses

  } catch (error) {
    console.error("Login error:", error);
    throw error; // Re-throw for error handling in calling code
  }
}

/**
 * Fetches the authentication token stored in the cookie from the server.
 * 
 * @returns A promise resolving to the token string, or null if fetching failed.
 */
export async function getToken(): Promise<string | null> {
  try {
    const response = await fetch('https://alpha2.gmt.bio/fetchCookie', {
      method: 'GET',
      credentials: 'include',
    });

    if (!response.ok) {
      throw new Error('Failed to fetch token');
    }
    const data = await response.json();
    return data.token; // Extract the token string here
  } catch (error) {
    console.error('Error fetching token:', error);
    return null;
  }
}

/**
 * Logs out the current user by calling the gRPC logout method and the logout endpoint.
 * Clears user info, login status and window theme after successful logout.
 */
export async function logout() {
  const token = await getToken();

  if (token && token.trim() !== "") {
    try {
      const response = await client.logout({ token }, await callOptionsUserToken());
    } catch (error) {
      console.error("Error while logout User:", error);
    }
  } else {
    console.warn("No token available. Skipping gRPC logout.");
  }

  try {
    const response = await fetch('https://alpha2.gmt.bio/logout', {
      method: 'POST',
      credentials: 'include',
    });

    if (!response.ok) {
      throw new Error('Cookie delete failed');
    }

    isLoggedIn.set(false);
    localStorage.removeItem('theme'); 
    localStorage.removeItem('username'); 
  } catch (error) {
    console.error('Cookie delete error:', error);
  }
}

/**
 * Returns the RPC call options containing the Authorization header with the user's JWT token.
 * 
 * @returns A promise resolving to the RpcOptions object with Authorization metadata.
 */
export async function callOptionsUserToken(): Promise<RpcOptions> {
  const userToken = await getToken();
  return {
    meta: {
      Authorization: `Bearer ${userToken}`
    }
  };
}

/**
 * Retrieves a worker token using the session token fetched from cookies.
 * First fetches the session token, then fetches the worker token with the session token as Authorization.
 * 
 * @returns A promise resolving to the worker token string, or null if retrieval failed.
 */
export async function getWorkerToken(): Promise<string | null> {
  try {
    const res = await fetch("https://alpha2.gmt.bio/fetchCookie", {
      credentials: "include",
    });

    if (!res.ok) throw new Error("Failed to get session token");

    const { token } = await res.json();

    const workerRes = await fetch("https://alpha2.gmt.bio/WorkerToken", {
      method: "GET",
      headers: {
        Authorization: `Bearer ${token}`, // Pass JWT manually here
      },
    });

    if (!workerRes.ok) throw new Error("Failed to get worker token");

    const { worker_token } = await workerRes.json();
    return worker_token;
  } catch (e) {
    console.error("Error getting worker token:", e);
    return null;
  }
}

/**
 * Returns the RPC call options containing the Authorization header with the worker's JWT token.
 * 
 * @returns A promise resolving to the RpcOptions object with Authorization metadata.
 */
export async function callOptionsWorkerToken(): Promise<RpcOptions> {
  const token = await getWorkerToken();
  return {
    meta: {
      Authorization: token ? `Bearer ${token}` : '',
    },
  };
}
