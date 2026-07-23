# 🌌 QIngest: Markdown to Qdrant Embedder

![QIngest Logo](./logo.png#width=500)

Transform your directory of Markdown files into a searchable, vector-indexed Qdrant knowledge base. Written in Go, QIngest provides intelligent change detection, semantic chunking, hybrid retrieval support, and an OpenAI-compatible embedding API.

---

## ✨ Key Features

- **🚀 Intelligent Idempotency:** Uses SHA256 hashing to detect file changes. Only new or modified files are processed and updated in the DB.
- **🧠 Semantic Chunking:** Leverages `langchaingo`'s `MarkdownTextSplitter` for high-quality Markdown parsing and context-aware text splitting.
- **⚡ Batch Processing:** Optimized document-level and chunk-level batching for high-performance ingestion.
- **🔌 OpenAI Compatible:** Works with any embedding server providing an OpenAI-compatible `/v1/embeddings` endpoint.
- **🧊 Qdrant Integration:** Full support for Qdrant vector collections, automated index creation, and metadata payload storage.
- **🔍 Hybrid Retrieval:** Optional sparse vector generation (token hashing + TF scoring) for RRF-based hybrid search.
- **🧹 Text Normalization:** Optional normalization that repairs broken line wrapping, removes control characters, and collapses excessive newlines.

---

## 🛠️ Prerequisites

- **Go 1.26+**
- A running **Qdrant** instance (`http://localhost:6333`).
- An **OpenAI-compatible embedding server** (e.g., LocalAI, Text-Embeddings-Inference, or OpenAI).

### Build

```bash
make build
```

This produces two binaries in `bin/`:

| Binary | Description |
|---|---|
| `bin/qingest` | Ingestion tool — discovers, chunks, embeds, and stores Markdown files into Qdrant. |
| `bin/qquery` | Query tool — embeds a search query and retrieves matching chunks from Qdrant. |

### Install Dependencies

```bash
go mod tidy
```

---

## ⚙️ Configuration

### Environment Variables (`.env`)

Create a `.env` file (see `.env.example`) to store your defaults. CLI flags always take precedence.

| Variable | Description | Default |
|:---|:---|:---|
| `QDRANT_URL` | Qdrant HTTP/REST URL | `http://localhost:6333` |
| `QDRANT_API_KEY` | Qdrant API Key (optional) | `None` |
| `QDRANT_COLLECTION` | Target collection name | `mdchunk` |
| `QDRANT_EMBED_URL` | Embedding API base URL | `http://127.0.0.1:8008/v1` |
| `QDRANT_EMBED_MODEL` | Embedding model name | `bge-m3` |

---

## 🚀 Quick Start

### 1. Initial Ingestion

Embed everything and set up the collection:

```bash
bin/qingest --dir ./docs --create-collection
```

### 2. Smart Sync (Incremental)

Only process new or modified files (detected via SHA256):

```bash
bin/qingest --dir ./docs
```

### 3. Force Refresh

Overwrite everything in the database:

```bash
bin/qingest --dir ./docs --force
```

### 4. Normalization Preview

Preview how normalization affects your documents without performing actual vector ingestion:

```bash
bin/qingest --dir ./docs --normalize --preview
```

### 5. Non-recursive (top-level only)

Only scan the top-level directory (skip subdirectories):

```bash
bin/qingest --dir ./docs --no-recursive
```

### 6. Verbose (debug) logging

```bash
bin/qingest --dir ./docs --verbose
```

### 7. Custom embed endpoint and model

```bash
bin/qingest --dir ./docs \
    --embed-url http://localai:80/v1 \
    --embed-model bge-large-en
```

### 8. Dry-run (no writes to Qdrant)

```bash
bin/qingest --dir ./docs --dry-run
```

### 9. Hybrid retrieval (dense + sparse vectors)

```bash
bin/qingest --dir ./docs --hybrid --create-collection
```

### 10. Limit to N documents

```bash
bin/qingest --dir ./docs --max-docs 10
```

---

## 🔍 Searching your Knowledge

Use the built-in `qquery` binary to test query search from the command line. It automatically loads your configuration, generates embeddings for your query, and searches Qdrant:

```bash
# Basic query (uses default settings from .env)
bin/qquery "How do I configure the server?"

# Specify Qdrant URL and collection
bin/qquery "How do I configure the server?" \
    --qdrant-url http://parma.sodalitas.net:6333 \
    --collection fffprose

# Filter out low-quality matches with a score threshold
bin/qquery "How do I configure the server?" \
    --score-threshold 0.5 \
    --limit 10

# Hybrid retrieval (requires collection created with --hybrid)
bin/qquery "How do I configure the server?" --hybrid
```

### qquery Flags

| Flag | Description | Default |
|:---|:---|:---|
| `--qdrant-url` | Qdrant API URL. | `http://localhost:6333` |
| `--qdrant-api-key` | Qdrant API Key (optional). | `None` |
| `--collection` | Qdrant collection to query. | `mdchunk` |
| `--embed-url` | Base URL of the OpenAI-compatible embedding API. | `http://127.0.0.1:8008/v1` |
| `--embed-model` | Embedding model name. | `bge-m3` |
| `--hybrid` | Enable hybrid retrieval (RRF fusion of dense + sparse). | — |
| `--limit` | Number of results to return. | `5` |
| `--score-threshold` | Minimum similarity score (0.0–1.0). Results below this are discarded. | `0.3` |

---

## 🧬 How It Works

### 🕵️ File Discovery & Hashing

The tool recursively walks your directory. For every file, it calculates a **SHA256 hash**.

- **New File:** Embedded and inserted.
- **Modified File:** Old points matching the file path are deleted, then new chunks are embedded and upserted.
- **Identical File:** Skipped entirely.

### ✂️ Semantic Chunking

Instead of simple line breaks, QIngest uses `langchaingo`'s `MarkdownTextSplitter`. This ensures that code blocks, headers, and lists are handled gracefully.

### 💾 Qdrant Payload Structure

Points are uploaded to Qdrant with:

- `id`: A random UUIDv4.
- `vector`: The embedding vector generated by the API.
- `payload`: A JSON object containing:
  - `content`: The raw text chunk.
  - `file_path`: Relative path for reference.
  - `file_hash`: Current file SHA256 hash.
  - `chunk_index`: The index of the chunk in the file.
  - `metadata`: Source and chunking metadata.
  - `indexed_at`: ISO format timestamp.

### 🔍 Hybrid Retrieval

When `--hybrid` is enabled, QIngest creates a collection with named vectors (`dense` and `sparse`). Sparse vectors are generated via token hashing (FNV-1a) with log-scaled term frequency scoring. Queries use Qdrant's RRF (Reciprocal Rank Fusion) to combine dense and sparse results.

### 🧹 Text Normalization

When `--normalize` is enabled, QIngest applies text normalization that:

- Removes BOM and non-printing control characters.
- Standardizes carriage returns (`\r\n` → `\n`).
- Repairs broken paragraph line wrapping (joins lines that don't end with a terminator and start lowercase, while respecting Markdown syntax).
- Collapses 3+ consecutive newlines to 2.

---

## 📦 Project Structure

```
qingest/
├── cmd/
│   ├── qingest/          # Ingestion CLI binary
│   └── qquery/           # Query CLI binary
├── internal/
│   ├── chunk/            # Markdown text splitting (langchaingo)
│   ├── config/           # CLI flag parsing + .env loading
│   ├── discover/         # File discovery (recursive .md walker)
│   ├── embed/            # OpenAI-compatible embedding client + sparse vectors
│   ├── normalize/        # Text normalization
│   ├── preview/          # Normalization diff preview
│   ├── progress/         # Terminal progress bar
│   └── qdrant/           # Qdrant REST client (collections, scroll, upsert, query)
├── bin/                  # Build output (gitignored)
├── go.mod
├── go.sum
├── Makefile
└── logo.png
```

---

## 🧪 Testing

```bash
make test
```

---

## 📜 License

This project is licensed under the [LICENSE](./LICENSE) file.
