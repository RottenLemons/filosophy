<script>
    let searchTerm = "";
    let files = [
        { name: "tax_report_2025.pdf", path: "/docs/finance" },
        { name: "deep_thoughts.txt", path: "/notes" },
        { name: "project_filosophy_final.docx", path: "/work/apps" }
    ];

    // This handles the "Google-like" instant filename filtering
    $: filteredResults = searchTerm 
        ? files.filter(f => f.name.toLowerCase().includes(searchTerm.toLowerCase())) 
        : [];

    let aiAnalysis = "";
    let loading = false;

    async function analyzeFile(fileName) {
        loading = true;
        // This is where you'd call your Backend/LLM
        const res = await fetch('http://localhost:3000/analyze', {
            method: 'POST',
            body: JSON.stringify({ filename: fileName })
        });
        const data = await res.json();
        aiAnalysis = data.summary;
        loading = false;
    }
</script>

<div class="google-container">
    <div class="search-section">
        <h1 class="logo">Filosophy</h1>
        <div class="search-input-wrapper">
            <input 
                type="text" 
                bind:value={searchTerm} 
                placeholder="Search filenames..." 
            />
        </div>
    </div>

    <main class="results">
        {#each filteredResults as file}
            <div class="result-item">
                <cite>{file.path}</cite>
                <h3 on:click={() => analyzeFile(file.name)}>{file.name}</h3>
                <button class="ai-btn" on:click={() => analyzeFile(file.name)}>
                    Ask LLM to explain this file
                </button>
            </div>
        {:else}
            {#if searchTerm}
                <p>No filenames match "{searchTerm}"</p>
            {/if}
        {/each}

        {#if loading}
            <p class="loading">Filosophy is thinking...</p>
        {:else if aiAnalysis}
            <div class="ai-response">
                <strong>AI Insight:</strong> {aiAnalysis}
            </div>
        {/if}
    </main>
</div>

<style>
    .google-container { font-family: sans-serif; max-width: 700px; margin: 50px auto; }
    .logo { color: #4285f4; text-align: center; font-size: 3rem; margin-bottom: 20px; }
    
    .search-input-wrapper {
        border: 1px solid #dfe1e5;
        border-radius: 24px;
        padding: 10px 20px;
        display: flex;
        box-shadow: 0 1px 6px rgba(32,33,36,0.2);
    }

    input { border: none; outline: none; width: 100%; font-size: 1rem; }
    
    .results { margin-top: 30px; }
    .result-item { margin-bottom: 25px; }
    
    cite { font-size: 0.8rem; color: #202124; font-style: normal; }
    h3 { color: #1a0dab; margin: 4px 0; cursor: pointer; font-weight: 400; }
    h3:hover { text-decoration: underline; }

    .ai-btn {
        background: #f8f9fa;
        border: 1px solid #f8f9fa;
        font-size: 0.8rem;
        padding: 5px 10px;
        border-radius: 4px;
        cursor: pointer;
    }

    .ai-response {
        margin-top: 20px;
        padding: 15px;
        background: #e8f0fe;
        border-radius: 8px;
        border-left: 4px solid #4285f4;
    }
</style>