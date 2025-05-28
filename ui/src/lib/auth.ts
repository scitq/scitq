import type { RpcOptions } from "@protobuf-ts/runtime-rpc";
import { get } from 'svelte/store';
import { userInfo, isLoggedIn } from '../lib/Stores/user';
import { client } from './grpcClient';

/**
 * Logs in a user with the provided username and password.
 * Sends a POST request to the login endpoint and updates user info and login status on success.
 * 
 * @param username - The username of the user.
 * @param password - The password of the user.
 * @returns A promise that resolves with the login response data or null if no JSON response.
 * @throws Throws an error if the login request fails or token is missing after login.
 */
export async function getLogin(username: string, password: string) {
  const response = await fetch('http://localhost:8081/login', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ username, password }),
    credentials: 'include', // To receive the cookie
  });

  if (!response.ok) {
    throw new Error('Login failed');
  }

  // Check if the response has a JSON content-type before parsing
  const contentType = response.headers.get('Content-Type');
  if (contentType && contentType.includes('application/json')) {
    const data = await response.json();
    const tokenCookie = await getToken();
    if (!tokenCookie) throw new Error('Token not found after login');
    userInfo.set({ token: tokenCookie });
    isLoggedIn.set(true);
    return data;
  } else {
    return null; // or {} depending on expected response
  }
}

/**
 * Fetches the authentication token stored in the cookie from the server.
 * 
 * @returns A promise resolving to the token string, or null if fetching failed.
 */
export async function getToken(): Promise<string | null> {
  try {
    const response = await fetch('http://localhost:8081/fetchCookie', {
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
 * Logs out the current user by calling the gRPC logout method and the REST logout endpoint.
 * Clears user info and login status after successful logout.
 */
export async function logout() {
  const token = get(userInfo).token;

  if (token && token.trim() !== "") {
    try {
      const response = await client.logout({ token }, await callOptionsUserToken());
      console.log("Logout gRPC success:", response);
    } catch (error) {
      console.error("Error while logout User:", error);
    }
  } else {
    console.warn("No token available. Skipping gRPC logout.");
  }

  try {
    const response = await fetch('http://localhost:8081/logout', {
      method: 'POST',
      credentials: 'include',
    });

    if (!response.ok) {
      throw new Error('Cookie delete failed');
    }

    userInfo.set({ token: null });
    isLoggedIn.set(false);
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
  let user = get(userInfo);
  return {
    meta: {
      Authorization: user?.token ? `Bearer ${user.token}` : '',
    },
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
    const res = await fetch("http://localhost:8081/fetchCookie", {
      credentials: "include",
    });

    if (!res.ok) throw new Error("Failed to get session token");

    const { token } = await res.json();

    const workerRes = await fetch("http://localhost:8081/WorkerToken", {
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
