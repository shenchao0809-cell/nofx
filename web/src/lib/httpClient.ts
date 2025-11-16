/**
 * HTTP Client with unified error handling and 401 interception
 *
 * Features:
 * - Unified fetch wrapper
 * - Automatic 401 token expiration handling
 * - Auth state cleanup on unauthorized
 * - Automatic redirect to login page
 * - Notification shown on login page after redirect
 * - Automatic CSRF token handling for POST/PUT/DELETE requests
 */

// CSRF Token 管理器
class CSRFTokenManager {
  private token: string | null = null
  private tokenPromise: Promise<string> | null = null

  async getToken(): Promise<string | null> {
    // 如果已有token，直接返回
    if (this.token) {
      return this.token
    }

    // 如果正在获取token，等待获取完成
    if (this.tokenPromise) {
      return this.tokenPromise
    }

    // 获取新token
    this.tokenPromise = this.fetchToken()
    const token = await this.tokenPromise
    this.tokenPromise = null
    return token
  }

  private async fetchToken(): Promise<string> {
    try {
      // CSRF token存储在HttpOnly Cookie中，前端无法直接读取
      // 需要从API获取token（API会从Cookie中读取并返回）
      const response = await fetch('/api/csrf-token', {
        method: 'GET',
        credentials: 'include', // 重要：包含Cookie
      })
      if (response.ok) {
        const data = await response.json()
        // API返回的token应该和Cookie中的token一致
        this.token = data.csrf_token || data.token
        if (this.token) {
          return this.token
        }
      }
    } catch (err) {
      console.error('Failed to fetch CSRF token:', err)
    }
    return ''
  }

  clearToken() {
    this.token = null
    this.tokenPromise = null
  }
}

const csrfTokenManager = new CSRFTokenManager()

export class HttpClient {
  // Singleton flag to prevent duplicate 401 handling
  private static isHandling401 = false

  /**
   * Reset 401 handling flag (call after successful login)
   */
  public reset401Flag(): void {
    HttpClient.isHandling401 = false
  }

  /**
   * Response interceptor - handles common HTTP errors
   *
   * @param response - Fetch Response object
   * @returns Response if successful
   * @throws Error with user-friendly message
   */
  private async handleResponse(response: Response): Promise<Response> {
    // Handle 401 Unauthorized - Token expired or invalid
    if (response.status === 401) {
      // Prevent duplicate 401 handling when multiple API calls fail simultaneously
      if (HttpClient.isHandling401) {
        throw new Error('登录已过期，请重新登录')
      }

      // Set flag to prevent race conditions
      HttpClient.isHandling401 = true

      // Clean up local storage
      localStorage.removeItem('auth_token')
      localStorage.removeItem('auth_user')

      // Notify global listeners (AuthContext will react to this)
      window.dispatchEvent(new Event('unauthorized'))

      // Only redirect if not already on login page
      if (!window.location.pathname.includes('/login')) {
        // Save current location for post-login redirect
        const returnUrl = window.location.pathname + window.location.search
        if (returnUrl !== '/login' && returnUrl !== '/') {
          sessionStorage.setItem('returnUrl', returnUrl)
        }

        // Mark that user came from 401 (login page will show notification)
        sessionStorage.setItem('from401', 'true')

        // Redirect immediately to login page
        window.location.href = '/login'

        // Return pending promise to prevent error from being caught by SWR/React
        // The notification will be shown on the login page
        return new Promise(() => {}) as Promise<Response>
      }

      throw new Error('登录已过期，请重新登录')
    }

    // Handle 403 Forbidden - 可能是CSRF token问题
    if (response.status === 403) {
      // 清除CSRF token，下次请求时重新获取
      csrfTokenManager.clearToken()
      throw new Error('没有权限访问此资源（可能是CSRF token问题，请重试）')
    }

    if (response.status === 404) {
      throw new Error('请求的资源不存在')
    }

    if (response.status >= 500) {
      throw new Error('服务器错误，请稍后重试')
    }

    return response
  }

  /**
   * GET request
   */
  async get(url: string, headers?: Record<string, string>): Promise<Response> {
    const response = await fetch(url, {
      method: 'GET',
      headers,
    })
    return this.handleResponse(response)
  }

  /**
   * POST request
   */
  async post(
    url: string,
    body?: any,
    headers?: Record<string, string>
  ): Promise<Response> {
    // 获取CSRF token
    const csrfToken = await csrfTokenManager.getToken()
    const requestHeaders: Record<string, string> = {
      'Content-Type': 'application/json',
      ...headers,
    }
    
    // 添加CSRF token到请求头
    if (csrfToken) {
      requestHeaders['X-CSRF-Token'] = csrfToken
    }

    const response = await fetch(url, {
      method: 'POST',
      headers: requestHeaders,
      credentials: 'include', // 包含cookies（CSRF token可能在cookie中）
      body: body ? JSON.stringify(body) : undefined,
    })
    return this.handleResponse(response)
  }

  /**
   * PUT request
   */
  async put(
    url: string,
    body?: any,
    headers?: Record<string, string>
  ): Promise<Response> {
    // 获取CSRF token
    const csrfToken = await csrfTokenManager.getToken()
    const requestHeaders: Record<string, string> = {
      'Content-Type': 'application/json',
      ...headers,
    }
    
    // 添加CSRF token到请求头
    if (csrfToken) {
      requestHeaders['X-CSRF-Token'] = csrfToken
    }

    const response = await fetch(url, {
      method: 'PUT',
      headers: requestHeaders,
      credentials: 'include', // 包含cookies（CSRF token可能在cookie中）
      body: body ? JSON.stringify(body) : undefined,
    })
    return this.handleResponse(response)
  }

  /**
   * DELETE request
   */
  async delete(
    url: string,
    headers?: Record<string, string>
  ): Promise<Response> {
    // 获取CSRF token
    const csrfToken = await csrfTokenManager.getToken()
    const requestHeaders: Record<string, string> = {
      ...headers,
    }
    
    // 添加CSRF token到请求头
    if (csrfToken) {
      requestHeaders['X-CSRF-Token'] = csrfToken
    }

    const response = await fetch(url, {
      method: 'DELETE',
      headers: requestHeaders,
      credentials: 'include', // 包含cookies（CSRF token可能在cookie中）
    })
    return this.handleResponse(response)
  }

  /**
   * Generic request method for custom configurations
   */
  async request(url: string, options: RequestInit = {}): Promise<Response> {
    const response = await fetch(url, options)
    return this.handleResponse(response)
  }
}

// Export singleton instance
export const httpClient = new HttpClient()

// Export helper function to reset 401 flag
export const reset401Flag = () => httpClient.reset401Flag()
