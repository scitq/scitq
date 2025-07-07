<script lang="ts">
  export let line1: string[];
  export let line2: string[];
  export let color1: string;
  export let color2: string;
  export let title1: string;
  export let title2: string;
  export let value1: string;
  export let value2: string;
  export let total1: string;
  export let total2: string;
  export let zoomLevel: number;
  export let autoZoom: boolean;
  export let zoomIntensity = 1; 
  export let onZoom: (direction: 'in' | 'out' | 'reset') => void;
  export let onToggleAutoZoom: () => void;
</script>

<div class="chart-container" >
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
  
<svg class="time-chart" width="100%" height="80" viewBox="0 0 300 80" preserveAspectRatio="xMidYMid meet">
  <!-- Baseline (y=0) -->
  <line x1="0" y1="80" x2="300" y2="80" stroke="#eee" stroke-width="1" />
  
  <!-- Data lines -->
  <polyline
    fill="none"
    stroke={color1}
    stroke-width={2 * zoomIntensity}
    stroke-opacity={0.7 + (zoomIntensity * 0.3)}
    points={line1.join(' ')}
    vector-effect="non-scaling-stroke"
  />
    
  <polyline
    fill="none"
    stroke={color2}
    stroke-width={2 * zoomIntensity}
    stroke-opacity={0.7 + (zoomIntensity * 0.3)}
    points={line2.join(' ')}
    vector-effect="non-scaling-stroke"
  />
</svg>

<div class="chart-controls">
  <label class="toggle-auto-zoom {autoZoom ? 'active' : ''}" data-testid={title1.toLowerCase() + '-auto-zoom-toggle'}>
    <input type="checkbox" bind:checked={autoZoom} on:change={onToggleAutoZoom}> 
    Auto Zoom {autoZoom ? '(ON)' : '(OFF)'}
  </label>

  {#if !autoZoom}
    <div class="manual-zoom">
      <button class="small-btn" on:click={() => onZoom('out')} title="Zoom Out" data-testid={title1.toLowerCase() + '-zoom-out'}>−</button>
      <span class="zoom-level" data-testid={title1.toLowerCase() + '-zoom-level'}>Zoom: {zoomLevel.toFixed(1)}x</span>
      <button class="small-btn" on:click={() => onZoom('in')} title="Zoom In" data-testid={title1.toLowerCase() + '-zoom-in'}>+</button>
      <button class="small-btn" on:click={() => onZoom('reset')} title="Reset Zoom" data-testid={title1.toLowerCase() + '-zoom-reset'}>↻</button>
    </div>
  {/if}
</div>
  
  <div class="chart-footer">
    <span>Total {title1}: {total1}</span>
    <span>Total {title2}: {total2}</span>
  </div>
</div>