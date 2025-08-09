<script lang="ts">
  /**
   * Array of points for the first data line in "x,y" format
   * @type {string[]}
   */
  export let line1: string[];

  /**
   * Array of points for the second data line in "x,y" format
   * @type {string[]}
   */
  export let line2: string[];

  /**
   * Color for the first data line
   * @type {string}
   */
  export let color1: string;

  /**
   * Color for the second data line
   * @type {string}
   */
  export let color2: string;

  /**
   * Title/label for the first data series
   * @type {string}
   */
  export let title1: string;

  /**
   * Title/label for the second data series
   * @type {string}
   */
  export let title2: string;

  /**
   * Current value for the first data series
   * @type {string}
   */
  export let value1: string;

  /**
   * Current value for the second data series
   * @type {string}
   */
  export let value2: string;

  /**
   * Total/cumulative value for the first data series
   * @type {string}
   */
  export let total1: string;

  /**
   * Total/cumulative value for the second data series
   * @type {string}
   */
  export let total2: string;

  /**
   * Current zoom level (multiplier)
   * @type {number}
   */
  export let zoomLevel: number;

  /**
   * Whether auto-zoom is enabled
   * @type {boolean}
   */
  export let autoZoom: boolean;

  /**
   * Intensity of the zoom effect (affects line width)
   * @type {number}
   * @default 1
   */
  export let zoomIntensity = 1;

  /**
   * Callback function for zoom actions
   * @type {(direction: 'in' | 'out' | 'reset') => void}
   */
  export let onZoom: (direction: 'in' | 'out' | 'reset') => void;

  /**
   * Callback function for toggling auto-zoom
   * @type {() => void}
   */
  export let onToggleAutoZoom: () => void;
</script>

<div class="chart-container">
  <!-- Chart header with legends -->
  <div class="chart-header">
    <div class="chart-legends">
      <span class="legend-item" style="color: {color1}">
        <span class="legend-dot" style="background: {color1}"></span>
        {title1}: {value1}
      </span>
      <span class="legend-item" style="color: {color2}">
        <span class="legend-dot" style="background: {color2}"></span>
        {title2}: {value2}
      </span>
    </div>
  </div>
  
  <!-- SVG chart container -->
  <svg class="time-chart" width="100%" height="80" viewBox="0 0 300 80" preserveAspectRatio="xMidYMid meet">
    <!-- Baseline (y=0) -->
    <line x1="0" y1="80" x2="300" y2="80" stroke="#eee" stroke-width="1" />
    
    <!-- First data line -->
    <polyline
      fill="none"
      stroke={color1}
      stroke-width={2 * zoomIntensity}
      stroke-opacity={0.7 + (zoomIntensity * 0.3)}
      points={line1.join(' ')}
      vector-effect="non-scaling-stroke"
    />
      
    <!-- Second data line -->
    <polyline
      fill="none"
      stroke={color2}
      stroke-width={2 * zoomIntensity}
      stroke-opacity={0.7 + (zoomIntensity * 0.3)}
      points={line2.join(' ')}
      vector-effect="non-scaling-stroke"
    />
  </svg>

  <!-- Chart controls section -->
  <div class="chart-controls">
    <!-- Auto-zoom toggle -->
    <label class="toggle-auto-zoom {autoZoom ? 'active' : ''}" data-testid={title1.toLowerCase() + '-auto-zoom-toggle'}>
      <input type="checkbox" bind:checked={autoZoom} on:change={onToggleAutoZoom}> 
      Auto Zoom {autoZoom ? '(ON)' : '(OFF)'}
    </label>

    <!-- Manual zoom controls (shown when auto-zoom is off) -->
    {#if !autoZoom}
      <div class="manual-zoom">
        <button 
          class="small-btn" 
          on:click={() => onZoom('out')} 
          title="Zoom Out" 
          data-testid={title1.toLowerCase() + '-zoom-out'}
        >
          −
        </button>
        <span class="zoom-level" data-testid={title1.toLowerCase() + '-zoom-level'}>
          Zoom: {zoomLevel.toFixed(1)}x
        </span>
        <button 
          class="small-btn" 
          on:click={() => onZoom('in')} 
          title="Zoom In" 
          data-testid={title1.toLowerCase() + '-zoom-in'}
        >
          +
        </button>
        <button 
          class="small-btn" 
          on:click={() => onZoom('reset')} 
          title="Reset Zoom" 
          data-testid={title1.toLowerCase() + '-zoom-reset'}
        >
          ↻
        </button>
      </div>
    {/if}
  </div>
  
  <!-- Chart footer with totals -->
  <div class="chart-footer">
    <span>Total {title1}: {total1}</span>
    <span>Total {title2}: {total2}</span>
  </div>
</div>