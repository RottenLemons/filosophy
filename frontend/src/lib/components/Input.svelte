<script>
  import { createEventDispatcher } from 'svelte';
  
  export let value = '';
  export let placeholder = '';
  export let type = 'text';
  export let disabled = false;
  export let id = Math.random().toString(36).substring(7); // default random ID
  
  const dispatch = createEventDispatcher();
  
  let focused = false;
  
  function handleInput(e) {
    value = e.target.value;
    dispatch('input', value);
  }
</script>

<!-- Editorial Input Field -->
<div class="relative group {$$props.class || ''}">
  <div class="relative flex items-center h-14 w-full bg-transparent transition-colors duration-200 
    {disabled ? 'opacity-50 cursor-not-allowed' : ''}">
    
    <!-- Prefix icon slot if any -->
    <div class="pr-4 text-outline z-10">
      <slot name="leadingIcon"></slot>
    </div>
    
    <input
      {id}
      {type}
      {value}
      {placeholder}
      {disabled}
      on:input={handleInput}
      on:focus={() => focused = true}
      on:blur={() => focused = false}
      on:keyup
      on:keydown
      class="peer w-full h-full bg-transparent pb-1 text-2xl font-headline placeholder:text-outline/40 focus:ring-0 focus:outline-none border-none border-0 px-0 z-10 {disabled ? 'pointer-events-none' : ''}"
    />
    
    <!-- Suffix icon slot if any -->
    <div class="pl-4 text-outline z-10">
      <slot name="trailingIcon"></slot>
    </div>
    
    <!-- Bottom Border (Rest) -->
    <div class="absolute bottom-0 left-0 right-0 h-[1px] bg-outline-variant transition-opacity duration-200 {focused ? 'opacity-0' : 'opacity-100'}"></div>
    
    <!-- Bottom Border (Focus Indicator) -->
    <div class="absolute bottom-0 left-0 right-0 h-[2px] bg-primary-container transition-transform duration-300 origin-center transform scale-x-0 {focused ? 'scale-x-100' : ''}"></div>
  </div>
</div>
